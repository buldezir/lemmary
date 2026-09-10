// Package metrics serves this process's OpenTelemetry metrics on a port of
// their own, so whatever scrapes them never holds a credential for the app.
//
// Off unless METRICS_ADDR is set, and no call site has to know that: the
// instruments below record into OpenTelemetry's global no-op meter until
// Register installs a provider, so instrumenting something is one
// unconditional line.
//
// This file is every instrument the app records and every attribute it labels
// them with. The names are a scrape-time contract that outlives any one
// dashboard, and cardinality is only controllable while it is all in one
// place -- so nothing here is labelled with a path, a document id or an
// account.
package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const serviceName = "lemmary"

// The constructors hand back a working no-op instrument alongside any error,
// so the error is genuinely nothing to handle: an unusable name is a bug fixed
// at compile time, not a condition to recover from at runtime. The one test in
// this package is what notices if a name stops appearing.
var (
	meter = otel.Meter(serviceName)

	jobDuration, _ = meter.Float64Histogram(
		"lemmary.job.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Wall time of one processing job run, by how it ended."),
	)
	aiCallDuration, _ = meter.Float64Histogram(
		"lemmary.ai.call.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Wall time of one outbound AI or OCR call, retries included."),
	)
	aiTokens, _ = meter.Int64Counter(
		"lemmary.ai.tokens",
		metric.WithDescription("Tokens a provider reported for a completion."),
	)

	jobsPending, _ = meter.Int64ObservableGauge(
		"lemmary.jobs.pending",
		metric.WithDescription("Processing jobs waiting to be claimed."),
	)

	// Two families rather than one, split by unit: a gauge carries a single
	// unit, and a series a dashboard cannot tell a document from a byte in is
	// worse than one extra metric name. Within a family, usage and limit share
	// the resource label, so "how full is it" is lemmary_usage / lemmary_limit.
	usageCount, _ = meter.Int64ObservableGauge(
		"lemmary.usage",
		metric.WithDescription("What the instance currently holds, by resource."),
	)
	usageBytes, _ = meter.Int64ObservableGauge(
		"lemmary.usage.bytes",
		metric.WithUnit("By"),
		metric.WithDescription("What the instance currently stores, by resource."),
	)
	limitCount, _ = meter.Int64ObservableGauge(
		"lemmary.limit",
		metric.WithDescription("What the instance is allowed to hold, by resource. Absent means unbounded."),
	)
	limitBytes, _ = meter.Int64ObservableGauge(
		"lemmary.limit.bytes",
		metric.WithUnit("By"),
		metric.WithDescription("What the instance is allowed to store, by resource. Absent means unbounded."),
	)
)

// Job records one finished pipeline job. outcome is the job's status after the
// run -- completed, needs_review, failed -- or retry, which is a run that
// failed a step and put itself back on the queue.
func Job(outcome string, d time.Duration) {
	jobDuration.Record(context.Background(), d.Seconds(), metric.WithAttributes(
		attribute.String("outcome", outcome),
	))
}

// TimeAICall starts the clock on one logical outbound provider call: one
// completion, one embedding batch, one text extraction -- retries and endpoint
// fallbacks included, since what the caller waited for is the whole of it.
// kind is chat, embed or ocr.
//
// Defer it and hand the deferred call the address of a named error return, so
// instrumenting a function that returns from several places stays one line:
//
//	func (c *Client) Do(ctx context.Context) (out T, err error) {
//		defer metrics.TimeAICall(ctx, "chat", c.sdk, model)(&err)
//
// A nil pointer counts as success, so a call site with nothing to report can
// pass one.
func TimeAICall(ctx context.Context, kind, sdk, model string) func(*error) {
	start := time.Now()
	return func(errp *error) {
		outcome := "ok"
		if errp != nil && *errp != nil {
			outcome = "error"
		}
		aiCallDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
			attribute.String("kind", kind),
			attribute.String("sdk", sdk),
			attribute.String("model", model),
			attribute.String("outcome", outcome),
		))
	}
}

// AITokens records what a completion cost. A provider that reports no usage
// produces three zeros, which is still worth having: it says the provider is
// not telling us. See ai.logUsage, its only caller.
//
// kind="cached" is the part of the prompt the provider served from its prefix
// cache. It is a subset of kind="prompt", not additional to it, so a bill
// built off this counts prompt + completion and reads cached as the discount.
func AITokens(model string, prompt, cached, completion int64) {
	add := func(kind string, n int64) {
		aiTokens.Add(context.Background(), n, metric.WithAttributes(
			attribute.String("model", model),
			attribute.String("kind", kind),
		))
	}
	add("prompt", prompt)
	add("cached", cached)
	add("completion", completion)
}

// QueueDepth registers a gauge that is read on every scrape rather than
// pushed, so nothing has to be kept in step with the queue as it moves.
func QueueDepth(fn func() int64) {
	_, _ = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(jobsPending, fn())
		return nil
	}, jobsPending)
}

// Usage is one reading of how full the instance is: what it holds, and what it
// is allowed to hold. Counts and byte figures are kept apart because they
// cannot share an instrument; see the gauges above.
//
// A resource missing from a limits map is unbounded and emits no series at
// all. Absence is the only honest way to say it: 0 is a limit somebody
// genuinely sells -- LIMIT_ADDITIONAL_USERS=0 is "this account and no others"
// -- so it cannot double as the unlimited sentinel, and -1 or +Inf would be a
// magic number every query would have to know about.
type Usage struct {
	Counts      map[string]int64
	CountLimits map[string]int64
	Bytes       map[string]int64
	ByteLimits  map[string]int64
}

// RegisterUsage registers the gauges for how full the instance is. read runs on
// every scrape, in one callback, because the numbers come from one measurement
// and a scrape should not pay for it four times.
func RegisterUsage(read func() (Usage, error)) {
	_, _ = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		u, err := read()
		if err != nil {
			// Returned, not swallowed: OpenTelemetry's error handler logs it,
			// and these four series go absent for the scrape rather than
			// reporting a zero that would read as an empty archive. The gap in
			// the graph is the signal.
			return err
		}
		observeByResource(o, usageCount, u.Counts)
		observeByResource(o, limitCount, u.CountLimits)
		observeByResource(o, usageBytes, u.Bytes)
		observeByResource(o, limitBytes, u.ByteLimits)
		return nil
	}, usageCount, limitCount, usageBytes, limitBytes)
}

func observeByResource(o metric.Observer, gauge metric.Int64ObservableGauge, values map[string]int64) {
	for resource, value := range values {
		o.ObserveInt64(gauge, value, metric.WithAttributes(
			attribute.String("resource", resource),
		))
	}
}
