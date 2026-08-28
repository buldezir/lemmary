package limits

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCheckFileBytes(t *testing.T) {
	lim := Limits{FileBytes: Of(1000)}

	if err := lim.CheckFile(1000, 1); err != nil {
		t.Fatalf("a file exactly at the limit was refused: %v", err)
	}

	err := lim.CheckFile(1001, 1)
	exceeded := AsExceeded(err)
	if exceeded == nil {
		t.Fatalf("an over-limit file was accepted, got %v", err)
	}
	if exceeded.Name != NameFileBytes {
		t.Fatalf("Name = %q, want %q", exceeded.Name, NameFileBytes)
	}
	if exceeded.Allowed != 1000 || exceeded.Used != 1001 {
		t.Fatalf("Allowed/Used = %d/%d, want 1000/1001", exceeded.Allowed, exceeded.Used)
	}
}

func TestCheckFilePages(t *testing.T) {
	lim := Limits{FilePages: Of(2)}

	if err := lim.CheckFile(1, 2); err != nil {
		t.Fatalf("a file exactly at the page limit was refused: %v", err)
	}

	exceeded := AsExceeded(lim.CheckFile(1, 3))
	if exceeded == nil {
		t.Fatal("a 3-page file passed a 2-page limit")
	}
	if exceeded.Name != NameFilePages {
		t.Fatalf("Name = %q, want %q", exceeded.Name, NameFilePages)
	}
	if !strings.Contains(exceeded.Message, "3 pages") {
		t.Fatalf("message does not say how many pages the file has: %q", exceeded.Message)
	}
}

// The byte limit is checked before the page limit, so the message names the
// reason a person can act on first.
func TestCheckFileReportsBytesBeforePages(t *testing.T) {
	lim := Limits{FileBytes: Of(10), FilePages: Of(1)}
	exceeded := AsExceeded(lim.CheckFile(100, 100))
	if exceeded == nil || exceeded.Name != NameFileBytes {
		t.Fatalf("want a %s rejection, got %+v", NameFileBytes, exceeded)
	}
}

func TestCheckRoomDocuments(t *testing.T) {
	lim := Limits{Documents: Of(3)}
	usage := Usage{Documents: 2}

	if err := lim.CheckRoom(usage, 1, 0, 0); err != nil {
		t.Fatalf("the third of three documents was refused: %v", err)
	}

	exceeded := AsExceeded(lim.CheckRoom(Usage{Documents: 3}, 1, 0, 0))
	if exceeded == nil {
		t.Fatal("a fourth document passed a limit of three")
	}
	if exceeded.Name != NameDocuments {
		t.Fatalf("Name = %q, want %q", exceeded.Name, NameDocuments)
	}
	if !strings.Contains(exceeded.Message, "3 of 3") {
		t.Fatalf("message does not show usage against the limit: %q", exceeded.Message)
	}
}

// A bulk path asks about many documents at once, and the message should say so
// rather than claiming there is no room for "another".
func TestCheckRoomBatchMessage(t *testing.T) {
	lim := Limits{Documents: Of(10)}
	exceeded := AsExceeded(lim.CheckRoom(Usage{Documents: 5}, 20, 0, 0))
	if exceeded == nil {
		t.Fatal("a 20-document batch passed with 5 of 10 used")
	}
	if !strings.Contains(exceeded.Message, "20 more") {
		t.Fatalf("message does not mention the batch size: %q", exceeded.Message)
	}
}

func TestCheckRoomPagesAndBytes(t *testing.T) {
	pages := Limits{DocumentPages: Of(100)}
	if err := pages.CheckRoom(Usage{DocumentPages: 90}, 1, 10, 0); err != nil {
		t.Fatalf("pages exactly at the limit were refused: %v", err)
	}
	if exceeded := AsExceeded(pages.CheckRoom(Usage{DocumentPages: 90}, 1, 11, 0)); exceeded == nil {
		t.Fatal("101 pages passed a 100-page limit")
	} else if exceeded.Name != NameDocumentPages {
		t.Fatalf("Name = %q, want %q", exceeded.Name, NameDocumentPages)
	}

	bytes := Limits{StorageBytes: Of(1 << 20)}
	if err := bytes.CheckRoom(Usage{StorageBytes: 1 << 19}, 1, 0, 1<<19); err != nil {
		t.Fatalf("bytes exactly at the limit were refused: %v", err)
	}
	if exceeded := AsExceeded(bytes.CheckRoom(Usage{StorageBytes: 1 << 20}, 1, 0, 1)); exceeded == nil {
		t.Fatal("one byte over the storage limit passed")
	} else if exceeded.Name != NameStorageBytes {
		t.Fatalf("Name = %q, want %q", exceeded.Name, NameStorageBytes)
	}
}

func TestCheckAdditionalUsers(t *testing.T) {
	lim := Limits{AdditionalUsers: Of(2)}

	if err := lim.CheckAdditionalUsers(2); err != nil {
		t.Fatalf("the second of two additional users was refused: %v", err)
	}
	exceeded := AsExceeded(lim.CheckAdditionalUsers(3))
	if exceeded == nil {
		t.Fatal("a third additional user passed a limit of two")
	}
	if exceeded.Name != NameAdditionalUsers {
		t.Fatalf("Name = %q, want %q", exceeded.Name, NameAdditionalUsers)
	}
}

// Zero additional users is a real plan, and it needs a message that does not
// read as a bug ("allows 0 accounts and already has that many").
func TestCheckAdditionalUsersZeroAllowance(t *testing.T) {
	lim := Limits{AdditionalUsers: Of(0)}
	exceeded := AsExceeded(lim.CheckAdditionalUsers(1))
	if exceeded == nil {
		t.Fatal("an additional user passed a limit of zero")
	}
	if strings.Contains(exceeded.Message, "0") {
		t.Fatalf("the zero-allowance message reads as an off-by-one: %q", exceeded.Message)
	}
}

// One account is free, and exactly one. The wizard's first admin must always
// get in, and the second account must not -- including the flagged users record
// a second superuser drags along, which is how an admin would otherwise mint
// seats without bound.
func TestAdditionalOfExemptsExactlyOneAccount(t *testing.T) {
	for _, tc := range []struct{ total, want int64 }{
		{0, 0}, {1, 0}, {2, 1}, {3, 2}, {10, 9},
	} {
		if got := additionalOf(tc.total); got != tc.want {
			t.Fatalf("additionalOf(%d) = %d, want %d", tc.total, got, tc.want)
		}
	}
}

func TestSeatLimitTraceOfEveryAccountCreate(t *testing.T) {
	solo := Limits{AdditionalUsers: Of(0)}

	// The setup wizard, on an empty instance.
	if err := solo.CheckAdditionalUsers(additionalOf(0 + 1)); err != nil {
		t.Fatalf("the first admin was refused on a solo plan: %v", err)
	}
	// Any second account, admin-flagged or not.
	if err := solo.CheckAdditionalUsers(additionalOf(1 + 1)); err == nil {
		t.Fatal("a second account passed a solo plan")
	}

	pair := Limits{AdditionalUsers: Of(2)}
	for _, total := range []int64{0, 1, 2} {
		if err := pair.CheckAdditionalUsers(additionalOf(total + 1)); err != nil {
			t.Fatalf("account %d of an admin-plus-two plan was refused: %v", total+1, err)
		}
	}
	if err := pair.CheckAdditionalUsers(additionalOf(3 + 1)); err == nil {
		t.Fatal("a fourth account passed an admin-plus-two plan")
	}
}

// The serialized form is what a client actually sees, and PocketBase rewrites
// any data value that does not implement router.SafeErrorItem into a generic
// "Invalid value." So assert on the JSON, not on RawData -- RawData passing
// proves nothing about what reaches the browser.
func TestExceededAPIErrorSurvivesSerialization(t *testing.T) {
	exceeded := &ErrExceeded{Name: NameDocuments, Allowed: 3, Used: 3, Message: "This instance holds 2 of 2 documents, so there is no room for another."}
	apiErr := exceeded.APIError()
	if apiErr.Status != 400 {
		t.Fatalf("Status = %d, want 400", apiErr.Status)
	}

	encoded, err := json.Marshal(apiErr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var payload struct {
		Message string `json:"message"`
		Data    struct {
			Limit struct {
				Code   string `json:"code"`
				Params struct {
					Limit   string `json:"limit"`
					Allowed int64  `json:"allowed"`
					Used    int64  `json:"used"`
				} `json:"params"`
			} `json:"limit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if payload.Message != "This instance holds 2 of 2 documents, so there is no room for another." {
		t.Fatalf("message = %q; a real message must survive PocketBase sentenizing it", payload.Message)
	}
	if payload.Data.Limit.Code != "limit_documents" {
		t.Fatalf("code = %q, want limit_documents (got %s)", payload.Data.Limit.Code, encoded)
	}
	if payload.Data.Limit.Params.Limit != NameDocuments {
		t.Fatalf("params.limit = %q, want %q", payload.Data.Limit.Params.Limit, NameDocuments)
	}
	if payload.Data.Limit.Params.Allowed != 3 || payload.Data.Limit.Params.Used != 3 {
		t.Fatalf("params allowed/used = %d/%d, want 3/3",
			payload.Data.Limit.Params.Allowed, payload.Data.Limit.Params.Used)
	}
}

func TestAsExceededIgnoresOtherErrors(t *testing.T) {
	if AsExceeded(nil) != nil {
		t.Fatal("nil produced a rejection")
	}
	if AsExceeded(errOther{}) != nil {
		t.Fatal("an unrelated error was read as a limit rejection")
	}
}

type errOther struct{}

func (errOther) Error() string { return "other" }

func TestFormatBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{20 << 20, "20 MB"},
		{1 << 30, "1.0 GB"},
	} {
		if got := formatBytes(tc.n); got != tc.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
