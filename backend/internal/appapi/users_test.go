package appapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestListUsersReturnsEveryAccountOldestFirst(t *testing.T) {
	app := bootQueueApp(t)
	first := makeQueueUser(t, app, "first@example.com")
	second := makeQueueUser(t, app, "second@example.com")

	users, err := listUsers(app)
	if err != nil {
		t.Fatalf("listUsers: %v", err)
	}
	if len(users) != 2 || users[0].ID != first || users[1].ID != second {
		t.Fatalf("users = %+v, want %s then %s", users, first, second)
	}
	if users[0].Email != "first@example.com" {
		t.Fatalf("email = %q", users[0].Email)
	}
}

func callManaged(t *testing.T, handler func(*core.RequestEvent) error, method, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{}
	e.Response = rec
	e.Request = httptest.NewRequest(method, "/api/app/admin/users", strings.NewReader(body))
	e.Request.SetPathValue("id", id)
	if err := handler(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	return rec
}

func TestManagedUsersCreateEditDeleteRegularAccountsOnly(t *testing.T) {
	app := bootQueueApp(t)
	admin := makeQueueUser(t, app, "admin@example.com")
	adminRecord, err := app.FindRecordById("users", admin)
	if err != nil {
		t.Fatal(err)
	}
	adminRecord.Set(pairedAdminField, true)
	if err := app.Save(adminRecord); err != nil {
		t.Fatal(err)
	}

	rec := callManaged(t, handleCreateManagedUser(app), http.MethodPost, "",
		`{"email":" new@example.com ","name":"New","password":"new-password-123"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var created managedUser
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Email != "new@example.com" || created.Name != "New" || created.Admin {
		t.Fatalf("created = %+v", created)
	}

	rec = callManaged(t, handleCreateManagedUser(app), http.MethodPost, "",
		`{"email":"new@example.com","password":"other-password-123"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate email = %d %s", rec.Code, rec.Body)
	}

	rec = callManaged(t, handlePatchManagedUser(app), http.MethodPatch, created.ID,
		`{"name":"Renamed","password":"changed-password-123"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	record, err := app.FindRecordById("users", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.GetString("name") != "Renamed" || !record.ValidatePassword("changed-password-123") {
		t.Fatalf("patch did not apply: name=%q", record.GetString("name"))
	}

	for _, call := range []struct {
		handler func(*core.RequestEvent) error
		method  string
	}{
		{handlePatchManagedUser(app), http.MethodPatch},
		{handleDeleteManagedUser(app), http.MethodDelete},
	} {
		rec = callManaged(t, call.handler, call.method, admin, `{"name":"Taken over"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s on an admin = %d %s", call.method, rec.Code, rec.Body)
		}
	}

	rec = callManaged(t, handleDeleteManagedUser(app), http.MethodDelete, created.ID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if _, err := app.FindRecordById("users", created.ID); err == nil {
		t.Fatal("user still exists after delete")
	}
	rec = callManaged(t, handleDeleteManagedUser(app), http.MethodDelete, created.ID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing = %d", rec.Code)
	}
}
