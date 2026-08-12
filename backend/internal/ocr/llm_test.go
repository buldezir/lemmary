package ocr

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openai/openai-go"
)

func TestLLMUserContentPartsImage(t *testing.T) {
	t.Parallel()
	parts, err := LLMUserContentParts("scan.png", "image/png", []byte("png-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("len=%d", len(parts))
	}
	raw, err := json.Marshal(openai.UserMessage(parts))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"type":"image_url"`) {
		t.Fatalf("expected image_url part: %s", body)
	}
	if !strings.Contains(body, "data:image/png;base64,") {
		t.Fatalf("expected image data URL: %s", body)
	}
	if strings.Contains(body, `"type":"file"`) {
		t.Fatalf("image should not use file part: %s", body)
	}
}

func TestLLMUserContentPartsPDF(t *testing.T) {
	t.Parallel()
	parts, err := LLMUserContentParts("invoice.pdf", "application/pdf", []byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(openai.UserMessage(parts))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"type":"file"`) {
		t.Fatalf("expected file part: %s", body)
	}
	if !strings.Contains(body, `"filename":"invoice.pdf"`) {
		t.Fatalf("expected filename: %s", body)
	}
	if !strings.Contains(body, "data:application/pdf;base64,") {
		t.Fatalf("expected file data URL: %s", body)
	}
}

func TestLLMUserContentPartsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := LLMUserContentParts("x", "image/png", nil); err == nil {
		t.Fatal("expected error")
	}
}
