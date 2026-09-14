package ocr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	vision "cloud.google.com/go/vision/v2/apiv1"
	gapioption "google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"lemmary/backend/internal/aiprovider"
)

// managedForTest turns AI_MANAGED on for the length of one test. Not
// parallel-safe; none of the tests below call t.Parallel().
func managedForTest(t *testing.T, on bool) {
	t.Helper()
	prev := aiprovider.Managed()
	aiprovider.SetManaged(on)
	t.Cleanup(func() { aiprovider.SetManaged(prev) })
}

// Docling and Mistral build their requests by hand rather than through an SDK,
// so neither gets the header from a middleware -- each has to stamp it itself.
func TestDoclingSendsDocumentHeaderOnlyWhenManaged(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		managed    bool
	}{
		{"managed", "doc123", true},
		{"self-hosted", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			managedForTest(t, tc.managed)
			var seen string
			server := newDoclingServerSeeing(t, &seen)
			provider := NewDoclingProvider(server.URL, "", "", 5*time.Second, nil)

			path := writeTempFile(t, "lemmary-doc-123.pdf", []byte("%PDF-1.7 fake"))
			ctx := aiprovider.WithDocument(context.Background(), "doc123")
			if _, err := provider.ExtractText(ctx, path, "application/pdf"); err != nil {
				t.Fatalf("ExtractText: %v", err)
			}
			if seen != tc.want {
				t.Errorf("%s = %q, want %q", aiprovider.DocumentHeader, seen, tc.want)
			}
		})
	}
}

// newDoclingServerSeeing answers a successful convert, recording the document
// header. Separate from newDoclingServer, which parses the multipart body this
// test does not care about.
func newDoclingServerSeeing(t *testing.T, seen *string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Get(aiprovider.DocumentHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, doclingBody("# ok"))
	}))
	t.Cleanup(server.Close)
	return server
}

// Vision is reached over gRPC, so its id travels as outgoing metadata rather
// than a header. The client itself needs a live service; the translation does
// not.
func TestVisionDocumentMetadataOnlyWhenManaged(t *testing.T) {
	for _, tc := range []struct {
		name    string
		managed bool
		doc     string
		want    []string
	}{
		{"managed, one document", true, "doc123", []string{"doc123"}},
		{"managed, no document", true, "", nil},
		{"self-hosted, one document", false, "doc123", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			managedForTest(t, tc.managed)
			ctx := withDocumentMetadata(aiprovider.WithDocument(context.Background(), tc.doc))
			md, _ := metadata.FromOutgoingContext(ctx)
			if got := md.Get(aiprovider.DocumentHeader); !slices.Equal(got, tc.want) {
				t.Errorf("%s metadata = %v, want %v", aiprovider.DocumentHeader, got, tc.want)
			}
		})
	}
}

func TestMistralSendsDocumentHeaderOnlyWhenManaged(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		managed    bool
	}{
		{"managed", "doc123", true},
		{"self-hosted", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			managedForTest(t, tc.managed)
			var seen string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Header.Get(aiprovider.DocumentHeader)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"pages": []map[string]any{{"markdown": "# ok"}},
				})
			}))
			t.Cleanup(server.Close)

			provider := NewMistralProvider("k", "mistral-ocr-latest", server.URL, 5*time.Second, slog.Default())
			path := writeTempFile(t, "lemmary-doc-123.pdf", []byte("%PDF-1.7 fake"))
			ctx := aiprovider.WithDocument(context.Background(), "doc123")
			if _, err := provider.ExtractText(ctx, path, "application/pdf"); err != nil {
				t.Fatalf("ExtractText: %v", err)
			}
			if seen != tc.want {
				t.Errorf("%s = %q, want %q", aiprovider.DocumentHeader, seen, tc.want)
			}
		})
	}
}

// visionMetadataServer answers any gRPC method with an error, recording the
// metadata the call carried. The Vision client needs a server to talk to; it
// does not need one that works, because the header is attached before the RPC
// and the failure is what comes back after it.
func visionMetadataServer(t *testing.T, seen *[]string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		md, _ := metadata.FromIncomingContext(stream.Context())
		*seen = md.Get(aiprovider.DocumentHeader)
		return status.Error(codes.Unimplemented, "test server")
	}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// The call-site test for Vision: withDocumentMetadata's own unit test would
// still pass if ExtractText stopped calling it.
func TestVisionExtractTextSendsDocumentMetadata(t *testing.T) {
	for _, tc := range []struct {
		name    string
		managed bool
		want    []string
	}{
		{"managed", true, []string{"doc123"}},
		{"self-hosted", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			managedForTest(t, tc.managed)
			var seen []string
			addr := visionMetadataServer(t, &seen)

			client, err := vision.NewImageAnnotatorClient(context.Background(),
				gapioption.WithEndpoint(addr),
				gapioption.WithoutAuthentication(),
				gapioption.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
			)
			if err != nil {
				t.Fatalf("vision client: %v", err)
			}
			t.Cleanup(func() { _ = client.Close() })

			provider := &GoogleVisionProvider{client: client, logger: slog.Default()}
			path := writeTempFile(t, "lemmary-doc-123.png", []byte("\x89PNG\r\n\x1a\n fake"))
			ctx := aiprovider.WithDocument(context.Background(), "doc123")
			// The server refuses every method, so this always errors; what it
			// was sent is the whole point.
			_, _ = provider.ExtractText(ctx, path, "image/png")

			if !slices.Equal(seen, tc.want) {
				t.Errorf("%s metadata = %v, want %v", aiprovider.DocumentHeader, seen, tc.want)
			}
		})
	}
}
