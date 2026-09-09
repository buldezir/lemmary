package aiprovider

import (
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// Binding names a provider row and a model, as a per-request or per-job
// replacement for one of the configured bindings in app_settings.
//
// A zero Binding means "use what Settings says", which is what every caller
// sent before overrides existed -- so an absent field and the old behaviour are
// the same thing, at every layer down to the stored job.
type Binding struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
}

// Empty reports a binding that overrides nothing.
//
// Keyed on the provider id alone: a model with no provider is not half an
// override, it is a request the resolver has to refuse. Treating it as empty
// would silently run the configured model instead of the one that was asked
// for, which is the one failure mode a picker must not have.
func (b Binding) Empty() bool {
	return strings.TrimSpace(b.ProviderID) == ""
}

// Normalized trims both fields, so a stored binding and one straight off the
// wire compare equal.
func (b Binding) Normalized() Binding {
	return Binding{
		ProviderID: strings.TrimSpace(b.ProviderID),
		Model:      strings.TrimSpace(b.Model),
	}
}

// Resolve loads the row a binding names and refuses one that cannot serve
// purpose. It returns a nil provider for an empty binding, which is not an
// error -- that is the "use Settings" case every caller starts from.
//
// This is the trust boundary: ProviderID arrives from a browser, on endpoints
// any signed-in user may call, and it decides which of the operator's
// credentials a request spends. Both halves are checked -- that the row is
// configured, and that its SDK can do this job -- because either one wrong is
// a request that fails deep inside a provider call with a message nobody can
// act on.
//
// The model is not checked against the catalogue. Settings has always offered a
// "Custom model id" field for exactly the model a provider added last week, so
// a name absent from /v1/models is legitimate; a wrong one surfaces as a
// provider error on the first call, as it does for the configured bindings.
func Resolve(app core.App, b Binding, purpose ModelPurpose) (*Provider, string, error) {
	b = b.Normalized()
	if b.Empty() {
		if b.Model != "" {
			return nil, "", fmt.Errorf("a model override needs a provider")
		}
		return nil, "", nil
	}

	p, err := FindByID(app, b.ProviderID)
	if err != nil || p == nil {
		return nil, "", fmt.Errorf("unknown provider %q", b.ProviderID)
	}
	if !p.Configured() {
		return nil, "", fmt.Errorf("provider %q is not configured", p.Alias)
	}
	if !ServesPurpose(p.SDK, purpose) {
		return nil, "", fmt.Errorf("provider %q cannot serve %s requests; want one of %v",
			p.Alias, purpose, PurposeSDKs(purpose))
	}
	if b.Model == "" && needsModel(p.SDK, purpose) {
		return nil, "", fmt.Errorf("provider %q needs a model", p.Alias)
	}
	return p, b.Model, nil
}

// needsModel reports whether a binding on this SDK has to name one.
//
// Almost always yes: a client built with an empty model sends an empty model,
// and what comes back is a provider error naming a field the user never saw.
// The configured model is not a sensible fallback either -- it belongs to a
// different provider, which need not serve it at all.
//
// The exception is the OCR binding on the two SDKs that read a document without
// being told a model: Google Vision has none to give, and for the local sidecar
// the field names an optional OCR engine rather than a model. Blank there is
// the correct configuration, which is what RequiresOCRModel already encodes.
func needsModel(sdk string, purpose ModelPurpose) bool {
	if purpose == PurposeOCR {
		return RequiresOCRModel(sdk)
	}
	return true
}

// ServesPurpose is the capability question the three predicates answer,
// dispatched by purpose rather than asked by name.
//
// It exists so a caller holding a ModelPurpose -- the models endpoint, the
// provider picker, Resolve -- does not switch on it itself. The frontend twin
// is providerServesPurpose in lib/api/providers.ts.
func ServesPurpose(sdk string, purpose ModelPurpose) bool {
	switch purpose {
	case PurposeOCR:
		return CanOCR(sdk)
	case PurposeEmbedding:
		return CanEmbed(sdk)
	default:
		return IsLLM(sdk)
	}
}

// PurposeSDKs names every SDK a purpose accepts, for the error messages that
// have to list them. Derived through sdksWhere like LLMSDKs and its siblings,
// so it cannot drift from the predicates.
func PurposeSDKs(purpose ModelPurpose) []string {
	return sdksWhere(func(sdk string) bool { return ServesPurpose(sdk, purpose) })
}
