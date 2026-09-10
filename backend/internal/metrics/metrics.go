package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// EnvAddr names the address the metrics endpoint listens on. Unset means off,
// and off is what every install had before this existed.
const EnvAddr = "METRICS_ADDR"

const (
	// The endpoint answers from memory, so a scraper that cannot send a
	// request header promptly is not one worth holding a connection for.
	readHeaderTimeout = 5 * time.Second
	// Shutdown is not allowed to hold the process: PocketBase's own graceful
	// stop gives the archive one second, and the scrape endpoint is worth less
	// than that.
	shutdownTimeout = time.Second
)

// Register serves this process's OpenTelemetry metrics on their own port.
//
// A no-op when METRICS_ADDR is unset. The address is read once, here, at
// wiring time, and the listener is bound from OnServe -- so `migrate`,
// `superuser` and every other subcommand open no port, and two instances of
// the app in one test binary do not fight over one.
func Register(app core.App) {
	addr := addrFromEnv()
	if addr == "" {
		return
	}

	var stop func(context.Context)

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		_, s, err := start(addr, e.App.Logger())
		if err != nil {
			// A metrics port that will not bind is not a reason to refuse to
			// serve the archive.
			e.App.Logger().Error("metrics endpoint disabled", "addr", addr, "error", err)
			return e.Next()
		}
		stop = s
		return e.Next()
	})

	// Default priority, so this runs after PocketBase's own graceful shutdown
	// (pbGracefulShutdown, priority -9999) and before its terminate finalizer
	// closes the databases the queue gauge reads.
	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		if stop != nil {
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			stop(ctx)
		}
		return e.Next()
	})
}

// addrFromEnv reads EnvAddr. A bare port ("9464") becomes ":9464" -- every
// interface -- because that is what a container reached through a published
// port needs, and it is how PORT already reads for the app itself. Spell out a
// host ("127.0.0.1:9464") to keep it on loopback instead; the endpoint is
// unauthenticated, so on a host that is not behind anything, do.
func addrFromEnv() string {
	addr := strings.TrimSpace(os.Getenv(EnvAddr))
	if addr == "" {
		return ""
	}
	if _, err := strconv.Atoi(addr); err == nil {
		return ":" + addr
	}
	return addr
}

// running is the one endpoint this process has, guarded because a second one
// cannot work: OpenTelemetry takes a single global MeterProvider, so every
// instrument in the binary feeds whichever provider was installed first, and a
// second endpoint would serve a page with nothing of ours on it. Rather than
// leave that as a silent puzzle, a repeat call returns what is already
// running.
//
// It matters outside tests too. The e2e harness boots a whole app repeatedly
// inside one test binary, and Register runs once per boot.
var running struct {
	sync.Mutex
	url  string
	stop func(context.Context)
}

// start installs the global MeterProvider and serves /metrics on addr. It
// returns the URL actually bound, which is what lets a test ask for port 0.
func start(addr string, logger *slog.Logger) (url string, stop func(context.Context), err error) {
	running.Lock()
	defer running.Unlock()
	if running.stop != nil {
		logger.Info("metrics endpoint already serving", "addr", running.url)
		return running.url, running.stop, nil
	}

	// First, and before anything is built: a port that will not bind must
	// leave nothing behind -- no registered collector, no global provider
	// accumulating measurements that nothing will ever read.
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}

	// A registry of our own rather than prometheus.DefaultRegisterer. The
	// default one is process-global and never unregistered from, so anything
	// that failed halfway would leave a collector wedged in it for the life of
	// the binary; this one is reachable only from here and from the handler
	// below. The two collectors are what the default registry would have given
	// for free: goroutines, heap, GC, open file descriptors.
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// WithoutScopeInfo drops the otel_scope_name/version/schema_url labels the
	// exporter would otherwise hang on every single series. There is one
	// instrumentation scope in this process, so they say nothing and make
	// every query and every label matcher longer.
	exporter, err := otelprom.New(
		otelprom.WithoutScopeInfo(),
		otelprom.WithRegisterer(registry),
	)
	if err != nil {
		_ = listener.Close()
		return "", nil, err
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(resource.NewSchemaless(attribute.String("service.name", serviceName))),
	)
	// Global, not passed down: this is also what switches on the
	// instrumentation already inside our dependencies -- the Google Vision
	// client's otelgrpc, which no code of ours could reach.
	otel.SetMeterProvider(provider)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	server := &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics endpoint stopped", "error", err)
		}
	}()
	logger.Info("metrics endpoint serving", "addr", listener.Addr().String(), "path", "/metrics")

	running.url = "http://" + listener.Addr().String() + "/metrics"
	running.stop = func(ctx context.Context) {
		_ = server.Shutdown(ctx)
		_ = provider.Shutdown(ctx)
	}
	return running.url, running.stop, nil
}
