package appapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

func requestEvent(t *testing.T) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{}
	e.Response = rec
	e.Request = httptest.NewRequest(http.MethodPost, "/api/app/providers/p1/chatgpt/device", nil)
	return e, rec
}

// Hiding the SDK in Settings is a courtesy; these endpoints stay reachable with
// any admin session, so the flag has to be enforced here or it is not enforced
// at all. Same reasoning as refuseWhenManaged.
func TestChatGPTEndpointsAreRefusedWhenTheFlagIsOff(t *testing.T) {
	t.Parallel()
	e, rec := requestEvent(t)
	refused, err := refuseWhenChatGPTDisabled(e, config.NewRuntime(config.AIEnv{}))
	if err != nil {
		t.Fatal(err)
	}
	if !refused {
		t.Fatal("a disabled instance served the ChatGPT sign-in")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	// The fix is a setting, not a retry, so the message has to name it.
	if !strings.Contains(rec.Body.String(), "AI_CHATGPT_LOGIN") {
		t.Errorf("the refusal does not say how to turn it on: %s", rec.Body.String())
	}
}

func TestChatGPTEndpointsAreServedWhenTheFlagIsOn(t *testing.T) {
	t.Parallel()
	e, _ := requestEvent(t)
	refused, err := refuseWhenChatGPTDisabled(e, config.NewRuntime(config.AIEnv{ChatGPTLogin: true}))
	if err != nil {
		t.Fatal(err)
	}
	if refused {
		t.Fatal("an instance that opted in still refused the sign-in")
	}
}
