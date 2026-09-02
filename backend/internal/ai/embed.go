package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/logfmt"
)

// Embedding request shaping. Every provider enforces some version of these
// limits and reports the breach as a 400, which no retry can fix -- so they are
// enforced here rather than discovered at the endpoint.
const (
	// maxBatchInputs is well under OpenAI's 2048-element array limit; the
	// binding constraint in practice is the per-request token total, not the
	// count.
	maxBatchInputs = 64
	// maxBatchRunes keeps one request under the 300k-token-per-request ceiling
	// with room to spare for scripts that tokenize badly.
	maxBatchRunes = 120000
	// maxInputRunes truncates a single input. Embedding models take 8192
	// tokens; 8000 runes is inside that for every script we index, and a chunk
	// is 1400 runes anyway -- this only ever bites on a header.
	maxInputRunes = 8000

	embedMaxAttempts = 4
	embedRetryBase   = time.Second
)

// EmbedResult is one Embed call's vectors plus what it cost. The counters are
// summed across the batches a single call had to make, so a caller logging them
// sees the real spend rather than the last request's.
type EmbedResult struct {
	Vectors      [][]float32
	PromptTokens int
	Requests     int
}

// Embedder turns text into vectors. Implementations are safe for concurrent use.
type Embedder interface {
	Name() string
	Model() string
	// Dims is the vector length, or 0 before the first response has been seen.
	Dims() int
	Embed(ctx context.Context, inputs []string) (EmbedResult, error)
}

// ErrDimsMismatch is returned when a provider answers with a different vector
// length than the one already recorded. It is not retryable and it must not be
// stored: rows of mixed length are silently dropped by the vector index, so the
// only correct response is to stop and let an admin re-confirm the model.
var ErrDimsMismatch = errors.New("embedding dimensions changed")

type openAIEmbedder struct {
	sdk     string
	model   string
	baseURL string
	client  openai.Client
	logger  *slog.Logger

	mu   sync.RWMutex
	dims int

	// sleep is time.Sleep in production; tests replace it so the backoff
	// schedule can be asserted without waiting for it.
	sleep func(time.Duration)
}

// NewEmbedder builds an embedding client on the OpenAI-compatible /embeddings
// endpoint. dims is the length already recorded for this model (0 when
// unknown); a response that disagrees with a non-zero value is refused.
func NewEmbedder(sdk, apiKey, model, baseURL string, dims int, timeout time.Duration, logger *slog.Logger) Embedder {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{Timeout: timeout}),
		option.WithRequestTimeout(timeout),
		// Retries are ours: only 429/5xx/network are worth repeating, and the
		// backoff has to be long enough to outlast a rate-limit window.
		option.WithMaxRetries(0),
	}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, option.WithBaseURL(strings.TrimRight(baseURL, "/")))
	}
	if strings.TrimSpace(sdk) == "" {
		sdk = aiprovider.SDKOpenAI
	}
	if logger == nil {
		logger = slog.Default()
	}
	if dims < 0 {
		dims = 0
	}

	return &openAIEmbedder{
		sdk:     sdk,
		model:   strings.TrimSpace(model),
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  openai.NewClient(opts...),
		logger:  logger,
		dims:    dims,
		sleep:   time.Sleep,
	}
}

func (e *openAIEmbedder) Name() string  { return e.sdk }
func (e *openAIEmbedder) Model() string { return e.model }

func (e *openAIEmbedder) Dims() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dims
}

// Embed returns one vector per input, in input order.
//
// A blank input is refused rather than skipped: the caller's chunk ordinals are
// positional, and quietly returning a short slice would misalign every vector
// after the gap.
func (e *openAIEmbedder) Embed(ctx context.Context, inputs []string) (EmbedResult, error) {
	if len(inputs) == 0 {
		return EmbedResult{}, nil
	}

	prepared := make([]string, len(inputs))
	for i, in := range inputs {
		if strings.TrimSpace(in) == "" {
			return EmbedResult{}, fmt.Errorf("embed input %d is blank", i)
		}
		prepared[i] = truncateRunes(in, maxInputRunes)
	}

	result := EmbedResult{Vectors: make([][]float32, len(prepared))}
	for _, batch := range batches(prepared) {
		vectors, tokens, err := e.embedBatch(ctx, prepared[batch.from:batch.to])
		result.Requests++
		result.PromptTokens += tokens
		if err != nil {
			return result, err
		}
		copy(result.Vectors[batch.from:batch.to], vectors)
	}
	return result, nil
}

type batchRange struct{ from, to int }

// batches groups inputs so no request exceeds either provider limit. Splitting
// on both counts matters: 64 header passages are small, while 64 body chunks of
// 1400 runes are not.
func batches(inputs []string) []batchRange {
	var out []batchRange
	from := 0
	runes := 0
	for i, in := range inputs {
		n := utf8.RuneCountInString(in)
		if i > from && (i-from >= maxBatchInputs || runes+n > maxBatchRunes) {
			out = append(out, batchRange{from: from, to: i})
			from = i
			runes = 0
		}
		runes += n
	}
	if from < len(inputs) {
		out = append(out, batchRange{from: from, to: len(inputs)})
	}
	return out
}

func (e *openAIEmbedder) embedBatch(ctx context.Context, inputs []string) ([][]float32, int, error) {
	params := openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: inputs},
		Model: e.model,
		// No `dimensions` parameter on purpose. Shortening a vector is a
		// per-model feature and would have to be stored alongside the model
		// name to stay reproducible; when the index RAM makes it worth it, add
		// it as a setting rather than a constant here.
	}

	var lastErr error
	for attempt := 0; attempt < embedMaxAttempts; attempt++ {
		if attempt > 0 {
			delay := embedRetryBase * time.Duration(1<<(2*(attempt-1))) // 1s, 4s, 16s
			e.logger.Warn("retrying embeddings request",
				"model", e.model, "attempt", attempt+1, logfmt.Duration("in", delay), slog.Any("error", lastErr))
			e.sleep(delay)
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
		}

		aiprovider.LogRequest(e.logger, e.sdk, http.MethodPost,
			aiprovider.EmbeddingsURL(e.baseURL), e.model, "inputs", len(inputs))

		resp, err := e.client.Embeddings.New(ctx, params)
		if err != nil {
			lastErr = err
			if !retryableEmbedError(err) {
				return nil, 0, fmt.Errorf("embeddings: %w", err)
			}
			continue
		}

		vectors, tokens, err := e.decode(resp, len(inputs))
		if err != nil {
			return nil, tokens, err
		}
		return vectors, tokens, nil
	}
	return nil, 0, fmt.Errorf("embeddings: %w", lastErr)
}

func (e *openAIEmbedder) decode(resp *openai.CreateEmbeddingResponse, want int) ([][]float32, int, error) {
	tokens := int(resp.Usage.PromptTokens)
	if len(resp.Data) != want {
		return nil, tokens, fmt.Errorf("embeddings: provider returned %d vectors for %d inputs", len(resp.Data), want)
	}

	// Reassemble by Index rather than by position: the response is documented
	// as unordered, and a shuffled batch would attach every vector to the wrong
	// chunk without anything ever erroring.
	vectors := make([][]float32, want)
	for _, item := range resp.Data {
		idx := int(item.Index)
		if idx < 0 || idx >= want {
			return nil, tokens, fmt.Errorf("embeddings: vector index %d is out of range", idx)
		}
		if vectors[idx] != nil {
			return nil, tokens, fmt.Errorf("embeddings: vector index %d returned twice", idx)
		}
		if len(item.Embedding) == 0 {
			return nil, tokens, fmt.Errorf("embeddings: vector %d is empty", idx)
		}
		if err := e.checkDims(len(item.Embedding)); err != nil {
			return nil, tokens, err
		}
		vec := make([]float32, len(item.Embedding))
		for i, v := range item.Embedding {
			vec[i] = float32(v)
		}
		vectors[idx] = vec
	}
	for i, v := range vectors {
		if v == nil {
			return nil, tokens, fmt.Errorf("embeddings: no vector for input %d", i)
		}
	}
	return vectors, tokens, nil
}

// checkDims records the vector length on the first response and refuses any
// later disagreement.
func (e *openAIEmbedder) checkDims(n int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dims == 0 {
		e.dims = n
		return nil
	}
	if e.dims != n {
		return fmt.Errorf("%w: model %s returned %d dimensions, expected %d", ErrDimsMismatch, e.model, n, e.dims)
	}
	return nil
}

// retryableEmbedError is true only for failures a second attempt could survive.
// A 400 (input too long, unknown model) and a 401 are answers, not outages, and
// repeating them just spends the backoff before failing anyway.
func retryableEmbedError(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	// Context cancellation is the caller giving up, not a transient fault.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Everything the SDK does not wrap in *openai.Error reached no endpoint:
	// DNS, connection refused, TLS, a read timeout mid-body.
	return true
}

// truncateRunes cuts without the human-facing ellipsis strutil.TruncateRunes
// appends: this text is fed to a model, not shown to anyone.
func truncateRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxRunes]))
}
