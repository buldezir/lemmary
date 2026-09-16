package aiprovider

import (
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// Binding names a provider row and a model, as a per-request or per-job
// replacement for one of the configured bindings in app_settings. A zero
// Binding means "use what Settings says", which is what every caller sent
// before overrides existed.
type Binding struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
}

// Empty is keyed on the provider id alone: a model with no provider is not
// half an override, it is a request the resolver has to refuse. Treating it as
// empty would silently run the configured model instead of the one asked for.
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
// purpose. A nil provider for an empty binding is the "use Settings" case, not
// an error.
//
// This is the trust boundary: ProviderID arrives from a browser and decides
// which of the operator's credentials a request spends. Both halves are
// checked, that the row is configured and that its SDK can do this job. The
// model is not checked against the catalogue: Settings has always allowed a
// custom model id, and a wrong one surfaces as a provider error on the first
// call.
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

// needsModel: almost always yes, because a client built with an empty model
// sends one, and what comes back is a provider error naming a field the user
// never saw. The exception is OCR on the SDKs that read a document without
// being told a model, which RequiresOCRModel encodes.
func needsModel(sdk string, purpose ModelPurpose) bool {
	if purpose == PurposeOCR {
		return RequiresOCRModel(sdk)
	}
	return true
}

// ServesPurpose dispatches the three capability predicates by purpose, so a
// caller holding a ModelPurpose does not switch on it itself. The frontend twin
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
