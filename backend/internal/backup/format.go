// Package backup defines the archive a user's whole library is exported to and
// restored from. Both sides of the round trip live here so the format cannot
// drift.
//
// The archive is a zip whose entries all live flat under one lemmary-export/
// folder, one group per document:
//
//	lemmary-export/manifest.json
//	lemmary-export/<date> [<id>] <title><ext>
//	lemmary-export/<date> [<id>] <title>.ocr.txt
//	lemmary-export/<date> [<id>] <title>.metadata.json
//	lemmary-export/<date> [<id>] <title>.preview.png
//
// <date> is the document's own YYYY-MM-DD, left out when it has none. Flat
// keeps the archive browsable by hand, and the date sorts it chronologically. The manifest names every entry
// explicitly, so the importer never has to guess whether "[id] Notes.ocr.txt"
// is a sidecar or a document whose own file happens to be a .txt.
package backup

// Version is bumped only for a change an older importer could not read
// correctly; additive fields do not bump it.
const (
	Format  = "lemmary-backup"
	Version = 1
)

const Root = "lemmary-export"

const ManifestName = Root + "/manifest.json"

// Sidecar suffixes appended to a document's entry base.
const (
	OCRSuffix      = ".ocr.txt"
	MetadataSuffix = ".metadata.json"
	PreviewSuffix  = ".preview.png"
)

// NamedEntity is a correspondent or document type. name_original is what
// worker.EnsureNamedEntity matches on, so keeping it lets a restore land on the
// same record instead of creating a near-duplicate.
type NamedEntity struct {
	Name         string `json:"name"`
	NameOriginal string `json:"name_original,omitempty"`
}

// Taxonomy includes records no document references, which are invisible in the
// per-document metadata and would otherwise not survive a round trip.
type Taxonomy struct {
	Tags           []string      `json:"tags"`
	Correspondents []NamedEntity `json:"correspondents"`
	DocumentTypes  []NamedEntity `json:"document_types"`
}

func (t Taxonomy) Count() int {
	return len(t.Tags) + len(t.Correspondents) + len(t.DocumentTypes)
}

// ManifestDocument paths are full entry names, including the Root prefix, so the
// importer can look them up verbatim. Absent sidecars are omitted.
type ManifestDocument struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	OCR      string `json:"ocr,omitempty"`
	Metadata string `json:"metadata,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

type Manifest struct {
	Format        string             `json:"format"`
	Version       int                `json:"version"`
	ExportedAt    string             `json:"exported_at"`
	DocumentCount int                `json:"document_count"`
	Taxonomy      Taxonomy           `json:"taxonomy"`
	Documents     []ManifestDocument `json:"documents"`
}
