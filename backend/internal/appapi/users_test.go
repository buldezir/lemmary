package appapi

import "testing"

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
