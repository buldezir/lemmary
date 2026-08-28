package backup

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// maxManifestBytes bounds the manifest read: it is a table of contents, and a
// crafted archive must not be able to make the importer inflate a gigabyte of
// JSON before it has decided anything.
const maxManifestBytes = 32 << 20

// ErrUnsupportedVersion is returned for an archive written by a newer Lemmary
// whose format this build cannot be trusted to read.
var ErrUnsupportedVersion = errors.New("the archive was written by a newer version of Lemmary")

// Group is the set of entries belonging to one document in the archive.
// Every path is a full entry name; the sidecars are empty when absent.
type Group struct {
	// ID is the document id in the instance the archive came from. It is used
	// to relate documents to each other (duplicate_of), never reused as the id
	// of the restored record.
	ID       string
	Title    string
	File     string
	OCR      string
	Metadata string
	Preview  string
}

// ReadManifest returns the archive's manifest, or nil for an archive written
// before manifests existed. A manifest that is present but unreadable is an
// error: silently falling back to name sniffing would import a subtly wrong
// library from an archive that is actually corrupt.
func ReadManifest(zr *zip.Reader) (*Manifest, error) {
	file, err := zr.Open(ManifestName)
	if err != nil {
		return nil, nil
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if int64(len(data)) > maxManifestBytes {
		return nil, errors.New("manifest is implausibly large")
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.Format != Format {
		return nil, fmt.Errorf("unexpected archive format %q", manifest.Format)
	}
	if manifest.Version > Version {
		return nil, ErrUnsupportedVersion
	}
	manifest.Taxonomy = normalizeTaxonomy(manifest.Taxonomy)
	return &manifest, nil
}

// Groups describes every document the archive holds, plus how many entries were
// ignored because they belong to no document.
//
// With a manifest the entry names are read verbatim, which is the only way to
// be certain about a document whose own file is a .txt named like an OCR
// sidecar. Without one — an archive from before manifests — the names are
// sniffed instead, and that one case is unresolvable.
func Groups(zr *zip.Reader, manifest *Manifest) (groups []Group, ignored int) {
	if manifest != nil {
		return groupsFromManifest(zr, manifest)
	}
	return sniffGroups(zr)
}

func groupsFromManifest(zr *zip.Reader, manifest *Manifest) (groups []Group, ignored int) {
	names := entryNames(zr)
	claimed := map[string]struct{}{ManifestName: {}}

	groups = make([]Group, 0, len(manifest.Documents))
	for _, doc := range manifest.Documents {
		if strings.TrimSpace(doc.ID) == "" || strings.TrimSpace(doc.File) == "" {
			continue
		}
		group := Group{ID: doc.ID, File: doc.File, Title: titleFromEntry(doc.File)}
		claimed[doc.File] = struct{}{}
		for _, sidecar := range []struct {
			path string
			into *string
		}{
			{doc.OCR, &group.OCR},
			{doc.Metadata, &group.Metadata},
			{doc.Preview, &group.Preview},
		} {
			if sidecar.path == "" {
				continue
			}
			claimed[sidecar.path] = struct{}{}
			if _, present := names[sidecar.path]; present {
				*sidecar.into = sidecar.path
			}
		}
		groups = append(groups, group)
	}

	for name := range names {
		if _, ok := claimed[name]; !ok {
			ignored++
		}
	}
	return groups, ignored
}

// sniffGroups reconstructs the documents of a manifest-less archive from the
// entry names alone.
func sniffGroups(zr *zip.Reader) (groups []Group, ignored int) {
	type bucket struct {
		group   Group
		entries int
	}
	buckets := map[string]*bucket{}
	order := make([]string, 0, len(zr.File))

	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || IsJunkEntry(name) {
			continue
		}
		rest, ok := strings.CutPrefix(name, Root+"/")
		if !ok || rest == "" || strings.Contains(rest, "/") || rest == "manifest.json" {
			ignored++
			continue
		}

		base, kind := classifyEntry(rest)
		id, title, ok := ParseEntryBase(base)
		if !ok {
			ignored++
			continue
		}

		b := buckets[base]
		if b == nil {
			b = &bucket{group: Group{ID: id, Title: title}}
			buckets[base] = b
			order = append(order, base)
		}
		b.entries++
		switch kind {
		case entryOCR:
			b.group.OCR = name
		case entryMetadata:
			b.group.Metadata = name
		case entryPreview:
			b.group.Preview = name
		default:
			b.group.File = name
		}
	}

	groups = make([]Group, 0, len(order))
	for _, base := range order {
		b := buckets[base]
		if b.group.File == "" {
			// Sidecars with nothing to attach to: not a document.
			ignored += b.entries
			continue
		}
		groups = append(groups, b.group)
	}
	return groups, ignored
}

type entryKind int

const (
	entryOriginal entryKind = iota
	entryOCR
	entryMetadata
	entryPreview
)

// classifyEntry splits a flat entry name into its document base and its role.
func classifyEntry(rest string) (base string, kind entryKind) {
	switch {
	case strings.HasSuffix(rest, MetadataSuffix):
		return strings.TrimSuffix(rest, MetadataSuffix), entryMetadata
	case strings.HasSuffix(rest, OCRSuffix):
		return strings.TrimSuffix(rest, OCRSuffix), entryOCR
	case strings.HasSuffix(rest, PreviewSuffix):
		return strings.TrimSuffix(rest, PreviewSuffix), entryPreview
	default:
		return strings.TrimSuffix(rest, path.Ext(rest)), entryOriginal
	}
}

// titleFromEntry recovers the sanitized title an entry name carries. It is a
// display fallback only — the real title comes from the metadata sidecar.
func titleFromEntry(entryPath string) string {
	rest := path.Base(entryPath)
	base, _ := classifyEntry(rest)
	_, title, ok := ParseEntryBase(base)
	if !ok {
		return ""
	}
	return title
}

func entryNames(zr *zip.Reader) map[string]struct{} {
	names := make(map[string]struct{}, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || IsJunkEntry(f.Name) {
			continue
		}
		names[f.Name] = struct{}{}
	}
	return names
}

// IsJunkEntry filters archiver bookkeeping (macOS resource forks, AppleDouble
// side files) that would otherwise look like real entries.
func IsJunkEntry(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "__MACOSX/") || strings.Contains(name, "/__MACOSX/") {
		return true
	}
	return strings.HasPrefix(path.Base(name), "._")
}
