package mailsink

import (
	"io"
	"net/mail"
	"reflect"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/tools/mailer"
)

func TestAddressesJSON(t *testing.T) {
	t.Parallel()

	got := addressesJSON([]mail.Address{
		{Name: "Ada", Address: "ada@example.com"},
		{Address: "bob@example.com"},
	})
	want := []addressDTO{
		{Name: "Ada", Address: "ada@example.com"},
		{Address: "bob@example.com"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("addressesJSON() = %#v, want %#v", got, want)
	}

	empty := addressesJSON(nil)
	if !reflect.DeepEqual(empty, []addressDTO{}) {
		t.Fatalf("addressesJSON(nil) = %#v, want empty slice", empty)
	}
}

func TestAttachmentNamesSorted(t *testing.T) {
	t.Parallel()

	message := &mailer.Message{
		Attachments: map[string]io.Reader{
			"z.txt": strings.NewReader("z"),
			"a.txt": strings.NewReader("a"),
		},
		InlineAttachments: map[string]io.Reader{
			"m.png": strings.NewReader("m"),
		},
	}
	got, ok := attachmentNames(message).([]string)
	if !ok {
		t.Fatalf("attachmentNames type %T", attachmentNames(message))
	}
	want := []string{"a.txt", "m.png", "z.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attachmentNames() = %v, want %v", got, want)
	}
}
