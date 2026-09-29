package appapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/testpb"
)

// Not parallel: NewRuntime writes the process-global managed flag.
func TestUnlessManagedRefusesOnlyAManagedInstance(t *testing.T) {
	prev := aiprovider.Managed()
	t.Cleanup(func() { aiprovider.SetManaged(prev) })

	for _, tc := range []struct {
		name    string
		managed bool
		want    int
	}{
		{"managed", true, http.StatusForbidden},
		{"self-hosted", false, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := config.NewRuntime(config.AIEnv{Managed: tc.managed})
			rec := httptest.NewRecorder()
			e := &core.RequestEvent{}
			e.Response = rec
			e.Request = httptest.NewRequest(http.MethodPost, "/api/app/ocr/test", nil)

			if err := unlessManaged(rt)(e); err != nil {
				t.Fatalf("middleware: %v", err)
			}
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// Not parallel, for the same reason. The collection id row is the one a path
// check by name alone would miss.
func TestLockSuperusersWhenManaged(t *testing.T) {
	prev := aiprovider.Managed()
	t.Cleanup(func() { aiprovider.SetManaged(prev) })

	app := testpb.Open(t)
	superusers, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		t.Fatalf("superusers collection: %v", err)
	}
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}

	for _, tc := range []struct {
		name       string
		path       string
		collection string
		auth       *core.Record
		managed    int
	}{
		{"dashboard", "/_/", "", nil, http.StatusNotFound},
		{"dashboard asset", "/_/images/logo.svg", "", nil, http.StatusNotFound},
		{"superuser sign-in by name", "/api/collections/_superusers/auth-with-password", core.CollectionNameSuperusers, nil, http.StatusForbidden},
		{"superuser sign-in by id", "/api/collections/" + superusers.Id + "/auth-with-password", superusers.Id, nil, http.StatusForbidden},
		{"a superuser token", "/api/settings", "", core.NewRecord(superusers), http.StatusForbidden},
		{"user sign-in", "/api/collections/users/auth-with-password", "users", nil, http.StatusOK},
		{"a user token", "/api/app/me", "", core.NewRecord(users), http.StatusOK},
	} {
		for _, managed := range []bool{true, false} {
			rt := config.NewRuntime(config.AIEnv{Managed: managed})
			rec := httptest.NewRecorder()
			e := &core.RequestEvent{App: app, Auth: tc.auth}
			e.Response = rec
			e.Request = httptest.NewRequest(http.MethodPost, tc.path, nil)
			if tc.collection != "" {
				e.Request.SetPathValue("collection", tc.collection)
			}

			if err := lockSuperusersWhenManaged(rt)(e); err != nil {
				t.Fatalf("%s: middleware: %v", tc.name, err)
			}
			want := http.StatusOK
			if managed {
				want = tc.managed
			}
			if rec.Code != want {
				t.Errorf("%s (managed %v): status = %d, want %d", tc.name, managed, rec.Code, want)
			}
		}
	}
}

// Not parallel, for the same reason.
func TestRefuseWritesWhenReadOnly(t *testing.T) {
	prev := aiprovider.Managed()
	t.Cleanup(func() { aiprovider.SetManaged(prev) })

	for _, tc := range []struct {
		method, path string
		passes       bool
	}{
		{http.MethodGet, "/api/collections/documents/records", true},
		{http.MethodPost, "/api/collections/users/auth-with-password", true},
		{http.MethodPost, "/api/collections/users/auth-refresh", true},
		{http.MethodPost, "/api/app/passkeys/login/finish", true},
		{http.MethodPost, "/api/realtime", true},
		{http.MethodPost, "/api/files/token", true},
		{http.MethodPost, "/api/collections/documents/records", false},
		{http.MethodPatch, "/api/app/settings", false},
		{http.MethodDelete, "/api/collections/documents/records/abc", false},
		{http.MethodPost, "/api/app/search", false},
		{http.MethodPost, "/api/app/passkeys/register/begin", false},
	} {
		for _, until := range []time.Time{{}, time.Now().Add(time.Hour), time.Now().Add(-time.Minute)} {
			rt := config.NewRuntime(config.AIEnv{Managed: true, WritableUntil: until})
			rec := httptest.NewRecorder()
			e := &core.RequestEvent{}
			e.Response = rec
			e.Request = httptest.NewRequest(tc.method, tc.path, nil)

			if err := refuseWritesWhenReadOnly(rt)(e); err != nil {
				t.Fatalf("%s %s: middleware: %v", tc.method, tc.path, err)
			}
			want := http.StatusOK
			if rt.ReadOnly() && !tc.passes {
				want = http.StatusForbidden
			}
			if rec.Code != want {
				t.Errorf("%s %s (until %v): status = %d, want %d", tc.method, tc.path, until, rec.Code, want)
			}
		}
	}
}
