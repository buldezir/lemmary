package worker

import "testing"

func TestEnsureNamedEntityRequiresUser(t *testing.T) {
	_, created, err := EnsureNamedEntity(nil, "correspondents", "", "Acme", "Acme")
	if err == nil {
		t.Fatal("expected error when user id is empty")
	}
	if created {
		t.Fatal("should not create without a user id")
	}
}

func TestEnsureNamedEntitySkipsEmptyName(t *testing.T) {
	id, created, err := EnsureNamedEntity(nil, "correspondents", "user1", "  ", "")
	if err != nil {
		t.Fatalf("empty name should be a no-op, got %v", err)
	}
	if id != "" || created {
		t.Fatalf("expected empty result, id=%q created=%v", id, created)
	}
}
