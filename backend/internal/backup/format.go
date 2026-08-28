// Package backup defines the archive a user's whole library is exported to and
// restored from.
//
// The archive is a zip whose entries all live under a single lemmary-export/
// folder, flat, one group per document:
//
//	lemmary-export/manifest.json
//	lemmary-export/[<id>] <title><ext>
//	lemmary-export/[<id>] <title>.ocr.txt
//	lemmary-export/[<id>] <title>.metadata.json
//	lemmary-export/[<id>] <title>.preview.png
//
// The flat layout is deliberate: the archive stays browsable by hand, which is
// half of why people take a backup at all. The manifest is what makes it
// machine-readable — it names every entry explicitly, so the importer never has
// to guess whether "[id] Notes.ocr.txt" is a sidecar or a document whose own
// file happens to be a .txt.
//
// Both sides of the round trip live here so the format cannot drift: the
// exporter writes through this package and the importer reads through it.
package backup

// Format and Version identify the archive. Version is bumped only for a change
// an older importer could not read correctly; additive fields do not bump it.
const (
	Format  = "lemmary-backup"
	Version = 1
)

// Root is the single folder every entry lives under.
const Root = "lemmary-export"

// ManifestName is the manifest's full entry path inside the archive.
const ManifestName = Root + "/manifest.json"

// Sidecar suffixes appended to a document's entry base.
const (
	OCRSuffix      = ".ocr.txt"
	MetadataSuffix = ".metadata.json"
	PreviewSuffix  = ".preview.png"
)

// NamedEntity is a correspondent or document type. name_original is the name as
// it was first seen on a document, which is what worker.EnsureNamedEntity
// matches on, so keeping it lets a restore land on the same record instead of
// creating a near-duplicate.
type NamedEntity struct {
	Name         string `json:"name"`
	NameOriginal string `json:"name_original,omitempty"`
}

// Taxonomy is every tag, correspondent and document type the user owns —
// including the ones no document references, which are invisible in the
// per-document metadata and would otherwise not survive a round trip.
type Taxonomy struct {
	Tags           []string      `json:"tags"`
	Correspondents []NamedEntity `json:"correspondents"`
	DocumentTypes  []NamedEntity `json:"document_types"`
}

// Count reports how many taxonomy records the archive carries.
func (t Taxonomy) Count() int {
	return len(t.Tags) + len(t.Correspondents) + len(t.DocumentTypes)
}

// ManifestDocument names the entries that belong to one document. Paths are
// full entry names (including the Root prefix) so the importer can look them up
// verbatim. Sidecars a document does not have are omitted.
type ManifestDocument struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	OCR      string `json:"ocr,omitempty"`
	Metadata string `json:"metadata,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

// Manifest is the archive's table of contents.
type Manifest struct {
	Format        string             `json:"format"`
	Version       int                `json:"version"`
	ExportedAt    string             `json:"exported_at"`
	DocumentCount int                `json:"document_count"`
	Taxonomy      Taxonomy           `json:"taxonomy"`
	Documents     []ManifestDocument `json:"documents"`
}
