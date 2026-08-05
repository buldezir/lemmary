package mail

import (
	"context"
	"strings"
	"testing"
)

type fakeClient struct {
	ids  []string
	msgs map[string]*Message
	atts map[string][]byte
}

func (f *fakeClient) ListMessageIDs(ctx context.Context, query string, max int) ([]string, error) {
	return append([]string{}, f.ids...), nil
}

func (f *fakeClient) GetMessage(ctx context.Context, id string, mode string) (*Message, error) {
	msg := f.msgs[id]
	if msg == nil {
		return nil, context.Canceled
	}
	cp := *msg
	if mode != ModeDeep {
		cp.BodyText = ""
	}
	return &cp, nil
}

func (f *fakeClient) DownloadAttachment(ctx context.Context, messageID, attachmentID string) ([]byte, error) {
	key := messageID + ":" + attachmentID
	data, ok := f.atts[key]
	if !ok {
		return nil, context.Canceled
	}
	return data, nil
}

type fakeTriager struct {
	decisions []ImportDecision
}

func (f *fakeTriager) Triage(ctx context.Context, mode string, messages []*Message) ([]ImportDecision, error) {
	return f.decisions, nil
}

func TestParseTriageResponseDeep(t *testing.T) {
	raw := `{"import":[{"message_id":"abc","filenames":["invoice.pdf"]}]}`
	got, err := ParseTriageResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Filenames[0] != "invoice.pdf" {
		t.Fatalf("%+v", got)
	}
}

func TestBuildTriagePayloadIncludesBodyInDeep(t *testing.T) {
	msgs := []*Message{{
		ID:       "1",
		Subject:  "Invoice",
		BodyText: "Please find attached",
		Attachments: []AttachmentMeta{{Filename: "a.pdf"}},
	}}
	simple := buildTriagePayload(ModeSimple, msgs)
	if _, ok := simple[0]["body"]; ok {
		t.Fatal("simple should omit body")
	}
	deep := buildTriagePayload(ModeDeep, msgs)
	if deep[0]["body"] != "Please find attached" {
		t.Fatalf("%v", deep[0]["body"])
	}
}

func TestTriageSystemPromptMentionsMode(t *testing.T) {
	if !strings.Contains(triageSystemPrompt(ModeDeep), "email body") {
		t.Fatal("deep prompt should mention body")
	}
	if !strings.Contains(triageSystemPrompt(ModeSimple), "no body") {
		t.Fatal("simple prompt should mention no body")
	}
}
