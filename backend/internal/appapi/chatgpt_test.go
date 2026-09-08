package appapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// The authorization code a poll returns is single-use, so the poll-exchange-
// save sequence has to run one at a time per provider: two overlapping polls
// would both read the same pending login, and the second exchange would be
// refused for a sign-in that had just succeeded.
//
// The handler itself is not exercised here -- it wants a live PocketBase, which
// nothing in this tree stands up -- so this covers the lock the handler holds.
func TestLoginPollsAreSerializedPerProvider(t *testing.T) {
	const id = "p-lock"
	var inside, overlaps atomic.Int32

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := lockLogin(id)
			defer unlock()
			if inside.Add(1) > 1 {
				overlaps.Add(1)
			}
			// Long enough that an unserialized run overlaps reliably.
			time.Sleep(2 * time.Millisecond)
			inside.Add(-1)
		}()
	}
	wg.Wait()

	if n := overlaps.Load(); n != 0 {
		t.Fatalf("%d polls ran concurrently for one provider", n)
	}
}

// Per provider, not globally: one operator signing in must not stall another's.
func TestLoginPollsOnDifferentProvidersDoNotBlockEachOther(t *testing.T) {
	firstHeld := lockLogin("p-lock-a")
	defer firstHeld()

	done := make(chan struct{})
	go func() {
		unlock := lockLogin("p-lock-b")
		unlock()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a poll on one provider blocked a poll on another")
	}
}
