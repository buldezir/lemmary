package worker

import (
	"testing"

	"lemmary/backend/internal/models"
)

func TestCorrespondentNameFromExplicitField(t *testing.T) {
	metadata := &models.ExtractedMetadata{
		Correspondent:         "Acme GmbH",
		PeopleOrOrganizations: []string{"Other Ltd."},
	}
	if got := correspondentName(metadata); got != "Acme GmbH" {
		t.Fatalf("expected Acme GmbH, got %q", got)
	}
}

func TestCorrespondentNameFallsBackToPeople(t *testing.T) {
	metadata := &models.ExtractedMetadata{
		PeopleOrOrganizations: []string{" ", "Acme Ltd."},
	}
	if got := correspondentName(metadata); got != "Acme Ltd." {
		t.Fatalf("expected Acme Ltd., got %q", got)
	}
}
