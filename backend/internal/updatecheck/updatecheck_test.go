package updatecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func serve(t *testing.T, routes map[string]any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	prevAPI, prevRegistry := githubAPI, registryURL
	githubAPI, registryURL = srv.URL, srv.URL
	t.Cleanup(func() { githubAPI, registryURL = prevAPI, prevRegistry })
}

func TestCheckForSkipsUnstampedBuilds(t *testing.T) {
	for _, v := range []string{"", "feat-x", "139/merge", "main", "v", "v1.x", "v01.2"} {
		if checkFor(v, "") != nil {
			t.Errorf("checkFor(%q, \"\") should not check", v)
		}
	}
	if checkFor("main", "abc") == nil || checkFor("v2026.10.13", "") == nil {
		t.Fatal("main and release builds should check")
	}
}

func TestCheckRelease(t *testing.T) {
	serve(t, map[string]any{"/repos/" + repo + "/releases/latest": map[string]string{"tag_name": "v2026.10.13"}})

	for running, want := range map[string]bool{"v2026.10.13": false, "v2026.10.14": false, "v2026.9.25": true, "v2026.10.7": true} {
		got, err := checkRelease(context.Background(), running)
		if err != nil {
			t.Fatal(err)
		}
		if got.Available != want {
			t.Errorf("running %s: available = %v, want %v", running, got.Available, want)
		}
		if want && (got.Version != "v2026.10.13" || got.URL != "https://github.com/"+repo+"/releases/tag/v2026.10.13") {
			t.Errorf("running %s: got %+v", running, got)
		}
	}
}

func TestCheckLatest(t *testing.T) {
	base := "/v2/" + repo
	serve(t, map[string]any{
		"/token": map[string]string{"token": "t"},
		base + "/manifests/latest": map[string]any{"manifests": []map[string]any{
			{"digest": "sha256:other", "platform": map[string]string{"os": "linux", "architecture": "s390x"}},
			{"digest": "sha256:mine", "platform": map[string]string{"os": "linux", "architecture": runtime.GOARCH}},
		}},
		base + "/manifests/sha256:mine": map[string]any{"config": map[string]string{"digest": "sha256:cfg"}},
		base + "/blobs/sha256:cfg":      map[string]any{"config": map[string]any{"Labels": map[string]string{revisionLabel: "new"}}},
	})

	for revision, want := range map[string]bool{"new": false, "old": true} {
		got, err := checkLatest(context.Background(), revision)
		if err != nil {
			t.Fatal(err)
		}
		if got.Available != want {
			t.Errorf("revision %s: available = %v, want %v", revision, got.Available, want)
		}
	}
}
