package worker

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
)

func makeProviderForOverrides(t *testing.T, app core.App, sdk, apiKey, baseURL string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		t.Fatalf("%s collection: %v", aiprovider.CollectionName, err)
	}
	record := core.NewRecord(collection)
	record.Set("sdk", sdk)
	// Unique per row: the collection has a unique index on alias.
	record.Set("alias", sdk+" "+apiKey+baseURL)
	record.Set("base_url", baseURL)
	record.Set("api_key", apiKey)
	if err := app.Save(record); err != nil {
		t.Fatalf("save provider: %v", err)
	}
	return record.Id
}

// jobWithOverrides is a job record carrying overrides but never saved -- what
// the create hook is handed before the write lands.
func jobWithOverrides(t *testing.T, app core.App, overrides config.Overrides) *core.Record {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatalf("processing_jobs collection: %v", err)
	}
	job := core.NewRecord(collection)
	job.Set("steps", []string{models.StepExtractMetadata})
	setJobOverrides(job, overrides)
	return job
}

// A job queued with no picker touched must look exactly like one queued before
// overrides existed: an unset column, not a stored "{}".
func TestEnqueueLeavesOverridesUnsetWhenThereAreNone(t *testing.T) {
	app := bootAppForEnqueue(t)
	documentID := makeDocumentForEnqueue(t, app)

	job, err := Enqueue(app, documentID, []string{models.StepEmbed}, nil, config.Overrides{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if raw := strings.TrimSpace(job.GetString(jobOverridesField)); raw != "" && raw != "null" {
		t.Fatalf("overrides = %q, want unset", raw)
	}
	if got := parseJobOverrides(job); !got.Empty() {
		t.Fatalf("parseJobOverrides() = %+v, want empty", got)
	}
}

// The choice has to survive the queue: the worker may not reach a job for
// minutes, and a batch queued to try a different extractor must not quietly run
// on whatever Settings holds by then.
func TestEnqueueStoresOverridesOnTheJob(t *testing.T) {
	app := bootAppForEnqueue(t)
	documentID := makeDocumentForEnqueue(t, app)
	provider := makeProviderForOverrides(t, app, aiprovider.SDKOpenAI, "sk-test", "https://api.openai.com/v1")

	want := aiprovider.Binding{ProviderID: provider, Model: "gpt-6-astra"}
	job, err := Enqueue(app, documentID, []string{models.StepExtractMetadata}, nil, config.Overrides{Extract: want})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Re-read rather than trusting the in-memory record: the round trip through
	// the JSON column is the part that can lose the value.
	stored, err := app.FindRecordById("processing_jobs", job.Id)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	got := parseJobOverrides(stored)
	if got.Extract != want {
		t.Fatalf("extract override = %+v, want %+v", got.Extract, want)
	}
	if !got.OCR.Empty() || !got.Embedding.Empty() {
		t.Fatalf("unset bindings came back populated: %+v", got)
	}
}

// A job whose overrides column holds something unreadable still runs, on the
// configured bindings -- the same answer parseForceSteps gives. The overrides
// are a refinement of an otherwise runnable job, and stranding a document over
// a malformed refinement is the worse failure.
func TestParseJobOverridesToleratesGarbage(t *testing.T) {
	app := bootAppForEnqueue(t)
	job := jobWithOverrides(t, app, config.Overrides{})
	job.Set(jobOverridesField, `"not an object"`)

	if got := parseJobOverrides(job); !got.Empty() {
		t.Fatalf("parseJobOverrides() = %+v, want empty", got)
	}
}

// The trust boundary. processing_jobs is writable by the document's owner --
// the single-document reprocess form creates a job straight through the
// collection API -- so a browser can name any provider row for any binding.
func TestValidateJobOverridesRefusesABindingTheProviderCannotServe(t *testing.T) {
	cases := map[string]struct {
		sdk       string
		baseURL   string
		overrides func(providerID string) config.Overrides
	}{
		// The embeddings sidecar embeds without chatting: bound to extraction it
		// would fail on every document.
		"local embeddings cannot extract": {
			sdk:     aiprovider.SDKLocalEmbeddings,
			baseURL: "http://embeddings:80/v1",
			overrides: func(id string) config.Overrides {
				return config.Overrides{Extract: aiprovider.Binding{ProviderID: id, Model: "bge-m3"}}
			},
		},
		// And the reverse: docling reads documents and cannot produce a vector.
		"docling cannot embed": {
			sdk:     aiprovider.SDKDocling,
			baseURL: "http://docling:5001",
			overrides: func(id string) config.Overrides {
				return config.Overrides{Embedding: aiprovider.Binding{ProviderID: id, Model: "rapidocr"}}
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			app := bootAppForEnqueue(t)
			provider := makeProviderForOverrides(t, app, tc.sdk, "", tc.baseURL)
			overrides := tc.overrides(provider)

			err := validateJobOverrides(app, config.Config{}, jobWithOverrides(t, app, overrides))
			if err == nil {
				t.Fatal("accepted an override whose provider cannot serve its binding")
			}
		})
	}
}

func TestValidateJobOverridesRefusesAnUnknownProvider(t *testing.T) {
	app := bootAppForEnqueue(t)
	job := jobWithOverrides(t, app, config.Overrides{
		Extract: aiprovider.Binding{ProviderID: "nosuchprovider", Model: "gpt-6-astra"},
	})

	if err := validateJobOverrides(app, config.Config{}, job); err == nil {
		t.Fatal("accepted an override naming a provider that does not exist")
	}
}

func TestValidateJobOverridesAcceptsAUsableExtractProvider(t *testing.T) {
	app := bootAppForEnqueue(t)
	provider := makeProviderForOverrides(t, app, aiprovider.SDKOpenAI, "sk-test", "https://api.openai.com/v1")
	job := jobWithOverrides(t, app, config.Overrides{
		Extract: aiprovider.Binding{ProviderID: provider, Model: "gpt-6-astra"},
	})

	if err := validateJobOverrides(app, config.Config{}, job); err != nil {
		t.Fatalf("validateJobOverrides: %v", err)
	}
}

// A job with nothing stored must not be made to resolve anything, so an
// instance with no providers at all still processes its uploads.
func TestValidateJobOverridesPassesWithNoOverrides(t *testing.T) {
	app := bootAppForEnqueue(t)
	if err := validateJobOverrides(app, config.Config{}, jobWithOverrides(t, app, config.Overrides{})); err != nil {
		t.Fatalf("validateJobOverrides: %v", err)
	}
}

// The embedding guard, end to end through the hook's entry point: the same
// model is a legitimate fix-up re-embed, a different one writes vectors the
// chunk index will never read.
func TestValidateJobOverridesEmbeddingModelMustMatchTheIndex(t *testing.T) {
	app := bootAppForEnqueue(t)
	provider := makeProviderForOverrides(t, app, aiprovider.SDKOpenAI, "sk-test", "https://api.openai.com/v1")
	cfg := config.Config{EmbeddingModel: "text-embedding-3-small"}

	same := jobWithOverrides(t, app, config.Overrides{
		Embedding: aiprovider.Binding{ProviderID: provider, Model: "text-embedding-3-small"},
	})
	if err := validateJobOverrides(app, cfg, same); err != nil {
		t.Fatalf("refused a re-embed on the configured model: %v", err)
	}

	other := jobWithOverrides(t, app, config.Overrides{
		Embedding: aiprovider.Binding{ProviderID: provider, Model: "text-embedding-3-large"},
	})
	if err := validateJobOverrides(app, cfg, other); err == nil {
		t.Fatal("accepted an embedding override the chunk index cannot read")
	}
}
