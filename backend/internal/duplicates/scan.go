package duplicates

import (
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/models"
)

// scanPageSize bounds how many records a scan holds in memory at once.
const scanPageSize = 200

// ScanResult summarizes a bulk duplicate scan.
type ScanResult struct {
	Scanned            int `json:"scanned"`
	ChecksumBackfilled int `json:"checksum_backfilled"`
	ExactMarked        int `json:"exact_marked"`
	NearMarked         int `json:"near_marked"`
	FingerprintsFilled int `json:"fingerprints_filled"`
}

// FindNearDuplicate returns the best matching earlier same-user document by OCR similarity.
// Only documents created before the current one (and not already marked as duplicates) are
// considered, so duplicate_of always points at the earlier original.
func FindNearDuplicate(app core.App, document *core.Record, ocrText string, threshold float64) (*core.Record, float64, error) {
	userID := document.GetString("user")
	created := strings.TrimSpace(document.GetString("created"))
	if userID == "" || created == "" || strings.TrimSpace(ocrText) == "" {
		return nil, 0, nil
	}
	fp := FingerprintHex(ocrText)
	if fp == "" {
		return nil, 0, nil
	}
	fpVal, ok := ParseFingerprintHex(fp)
	if !ok {
		return nil, 0, nil
	}

	var best *core.Record
	bestScore := 0.0
	offset := 0
	// Paginate rather than capping: with a fixed limit the newest candidates —
	// the most likely duplicates — would silently never be compared.
	for {
		candidates, err := app.FindRecordsByFilter(
			"documents",
			"user = {:user} && id != {:id} && text_fingerprint != '' && ocr_text != '' && duplicate_of = '' && created < {:created}",
			"created",
			scanPageSize,
			offset,
			map[string]any{"user": userID, "id": document.Id, "created": created},
		)
		if err != nil {
			return nil, 0, err
		}

		for _, candidate := range candidates {
			if !EligibleNearDuplicateOriginal(document, candidate) {
				continue
			}
			otherFP, ok := ParseFingerprintHex(candidate.GetString("text_fingerprint"))
			if !ok {
				continue
			}
			if HammingDistance(fpVal, otherFP) > MaxHammingDistance {
				continue
			}
			score := TextSimilarity(ocrText, candidate.GetString("ocr_text"))
			if score >= threshold && score > bestScore {
				best = candidate
				bestScore = score
			}
		}

		if len(candidates) < scanPageSize {
			return best, bestScore, nil
		}
		offset += scanPageSize
	}
}

// EligibleNearDuplicateOriginal reports whether candidate may be treated as the original
// for document (earlier, same timeline, not itself a duplicate).
func EligibleNearDuplicateOriginal(document, candidate *core.Record) bool {
	if document == nil || candidate == nil {
		return false
	}
	if candidate.GetString("duplicate_of") != "" {
		return false
	}
	docCreated := strings.TrimSpace(document.GetString("created"))
	candCreated := strings.TrimSpace(candidate.GetString("created"))
	if docCreated == "" || candCreated == "" {
		return false
	}
	return candCreated < docCreated
}

// MarkAsDuplicate links document to original and sets needs_review.
// marked is false when the document was already linked, so scan counters do not
// report a no-op as a fresh mark on every run.
func MarkAsDuplicate(app core.App, document, original *core.Record) (marked bool, err error) {
	if document == nil || original == nil {
		return false, fmt.Errorf("missing document for duplicate mark")
	}
	if document.GetString("duplicate_of") != "" {
		return false, nil
	}
	document.Set("duplicate_of", original.Id)
	document.Set("processing_status", models.DocStatusNeedsReview)
	if err := app.Save(document); err != nil {
		return false, err
	}
	return true, nil
}

// ScanAll backfills checksums/fingerprints and marks exact (and optional near) duplicates.
func ScanAll(app core.App, cfg config.Config) (ScanResult, error) {
	var result ScanResult
	threshold := cfg.NearDuplicateThreshold
	if threshold <= 0 || threshold > 1 {
		threshold = config.DefaultNearDuplicateThreshold
	}

	page := 1
	for {
		records, err := app.FindRecordsByFilter("documents", "id != ''", "created", 100, (page-1)*100, nil)
		if err != nil {
			return result, err
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			result.Scanned++
			if err := backfillChecksum(app, record, &result); err != nil {
				app.Logger().Warn("duplicate scan checksum failed", "document", record.Id, "error", err)
			}
			if err := backfillFingerprint(app, record, &result); err != nil {
				app.Logger().Warn("duplicate scan fingerprint failed", "document", record.Id, "error", err)
			}
		}
		if len(records) < 100 {
			break
		}
		page++
	}

	if err := markExactDuplicates(app, &result); err != nil {
		return result, err
	}
	if cfg.NearDuplicateDetectionEnabled {
		if err := markNearDuplicates(app, threshold, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func backfillChecksum(app core.App, record *core.Record, result *ScanResult) error {
	if strings.TrimSpace(record.GetString("checksum")) != "" {
		return nil
	}
	checksum, err := HashDocumentFile(app, record)
	if err != nil {
		return err
	}
	userID := record.GetString("user")
	existing, err := FindByChecksum(app, userID, checksum, record.Id)
	if err != nil {
		return err
	}
	if existing != nil {
		original, duplicate := earlierRecord(existing, record), laterRecord(existing, record)
		if original.Id == record.Id {
			// Older document missing checksum while a newer one already has it: take ownership.
			if existing.GetString("checksum") != "" {
				existing.Set("checksum", "")
				if err := app.Save(existing); err != nil {
					return err
				}
			}
			record.Set("checksum", checksum)
			if err := app.Save(record); err != nil {
				return err
			}
			result.ChecksumBackfilled++
			marked, err := MarkAsDuplicate(app, duplicate, original)
			if err != nil {
				return err
			}
			if marked {
				result.ExactMarked++
			}
			return nil
		}
		marked, err := MarkAsDuplicate(app, record, existing)
		if err != nil {
			return err
		}
		if marked {
			result.ExactMarked++
		}
		return nil
	}

	record.Set("checksum", checksum)
	if err := app.Save(record); err != nil {
		if !IsChecksumUniqueViolation(err) {
			return err
		}
		existing, findErr := FindByChecksum(app, userID, checksum, record.Id)
		if findErr != nil {
			return findErr
		}
		if existing == nil {
			return err
		}
		marked, markErr := MarkAsDuplicate(app, record, existing)
		if markErr != nil {
			return markErr
		}
		if marked {
			result.ExactMarked++
		}
		return nil
	}
	result.ChecksumBackfilled++
	return nil
}

func earlierRecord(a, b *core.Record) *core.Record {
	if strings.TrimSpace(a.GetString("created")) <= strings.TrimSpace(b.GetString("created")) {
		return a
	}
	return b
}

func laterRecord(a, b *core.Record) *core.Record {
	if earlierRecord(a, b) == a {
		return b
	}
	return a
}

func backfillFingerprint(app core.App, record *core.Record, result *ScanResult) error {
	if strings.TrimSpace(record.GetString("text_fingerprint")) != "" {
		return nil
	}
	ocrText := strings.TrimSpace(record.GetString("ocr_text"))
	if ocrText == "" {
		return nil
	}
	fp := FingerprintHex(ocrText)
	if fp == "" {
		return nil
	}
	record.Set("text_fingerprint", fp)
	if err := app.Save(record); err != nil {
		return err
	}
	result.FingerprintsFilled++
	return nil
}

func markExactDuplicates(app core.App, result *ScanResult) error {
	type groupKey struct {
		user     string
		checksum string
	}

	rows, err := scanRows(app, "checksum != ''")
	if err != nil {
		return err
	}

	// Group by (user, checksum) over lightweight rows so a large archive does not
	// pull every document (and its OCR text) into memory at once.
	groups := map[groupKey][]scanRow{}
	for _, row := range rows {
		key := groupKey{user: row.User, checksum: row.Checksum}
		groups[key] = append(groups[key], row)
	}

	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		original, err := app.FindRecordById("documents", group[0].ID)
		if err != nil {
			return err
		}
		for _, row := range group[1:] {
			if row.DuplicateOf != "" {
				continue
			}
			duplicate, err := app.FindRecordById("documents", row.ID)
			if err != nil {
				return err
			}
			marked, err := MarkAsDuplicate(app, duplicate, original)
			if err != nil {
				return err
			}
			if marked {
				result.ExactMarked++
			}
		}
	}
	return nil
}

func markNearDuplicates(app core.App, threshold float64, result *ScanResult) error {
	rows, err := scanRows(app, "ocr_text != '' AND text_fingerprint != ''")
	if err != nil {
		return err
	}

	byUser := map[string][]scanRow{}
	for _, row := range rows {
		byUser[row.User] = append(byUser[row.User], row)
	}

	// Rows carry only the SimHash, so the O(n²) sweep stays in memory-cheap
	// integer comparisons. OCR text is loaded only for the few pairs that pass
	// the Hamming prefilter, and the outer document's text is loaded once.
	marked := map[string]struct{}{}
	for _, group := range byUser {
		for i := 0; i < len(group); i++ {
			a := group[i]
			if a.DuplicateOf != "" || isMarked(marked, a.ID) {
				continue
			}
			fa, ok := ParseFingerprintHex(a.Fingerprint)
			if !ok {
				continue
			}

			var (
				aRecord *core.Record
				aText   string
			)
			for j := i + 1; j < len(group); j++ {
				b := group[j]
				if b.DuplicateOf != "" || isMarked(marked, b.ID) {
					continue
				}
				fb, ok := ParseFingerprintHex(b.Fingerprint)
				if !ok {
					continue
				}
				if HammingDistance(fa, fb) > MaxHammingDistance {
					continue
				}

				if aRecord == nil {
					aRecord, err = app.FindRecordById("documents", a.ID)
					if err != nil {
						return err
					}
					aText = aRecord.GetString("ocr_text")
				}
				bRecord, err := app.FindRecordById("documents", b.ID)
				if err != nil {
					return err
				}
				if TextSimilarity(aText, bRecord.GetString("ocr_text")) < threshold {
					continue
				}
				// Prefer older as original (group sorted by created ascending).
				linked, err := MarkAsDuplicate(app, bRecord, aRecord)
				if err != nil {
					return err
				}
				marked[b.ID] = struct{}{}
				if linked {
					result.NearMarked++
				}
			}
		}
	}
	return nil
}

func isMarked(marked map[string]struct{}, id string) bool {
	_, ok := marked[id]
	return ok
}

// scanRow is the projection the bulk scan works on: everything needed to group
// and prefilter, and nothing large. OCR text is fetched per record only when a
// pair actually has to be compared.
type scanRow struct {
	ID          string `db:"id"`
	User        string `db:"user"`
	Created     string `db:"created"`
	Checksum    string `db:"checksum"`
	Fingerprint string `db:"text_fingerprint"`
	DuplicateOf string `db:"duplicate_of"`
}

// scanRows pages through documents matching filter, oldest first.
func scanRows(app core.App, filter string) ([]scanRow, error) {
	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return nil, err
	}

	var rows []scanRow
	offset := 0
	for {
		var page []scanRow
		err := app.RecordQuery(collection).
			Select("id", "[[user]]", "created", "checksum", "text_fingerprint", "duplicate_of").
			AndWhere(dbx.NewExp(filter)).
			OrderBy("created ASC", "id ASC").
			Limit(int64(scanPageSize)).
			Offset(int64(offset)).
			All(&page)
		if err != nil {
			return nil, fmt.Errorf("list documents for duplicate scan: %w", err)
		}
		rows = append(rows, page...)
		if len(page) < scanPageSize {
			return rows, nil
		}
		offset += scanPageSize
	}
}
