package duplicates

import (
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/models"
)

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

	candidates, err := app.FindRecordsByFilter(
		"documents",
		"user = {:user} && id != {:id} && text_fingerprint != '' && ocr_text != '' && duplicate_of = '' && created < {:created}",
		"created",
		500,
		0,
		map[string]any{"user": userID, "id": document.Id, "created": created},
	)
	if err != nil {
		return nil, 0, err
	}

	var best *core.Record
	bestScore := 0.0
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
	return best, bestScore, nil
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
func MarkAsDuplicate(app core.App, document, original *core.Record) error {
	if document == nil || original == nil {
		return fmt.Errorf("missing document for duplicate mark")
	}
	if document.GetString("duplicate_of") != "" {
		return nil
	}
	document.Set("duplicate_of", original.Id)
	document.Set("processing_status", models.DocStatusNeedsReview)
	return app.Save(document)
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
			if duplicate.GetString("duplicate_of") == "" {
				if err := MarkAsDuplicate(app, duplicate, original); err != nil {
					return err
				}
				result.ExactMarked++
			}
			return nil
		}
		if err := MarkAsDuplicate(app, record, existing); err != nil {
			return err
		}
		result.ExactMarked++
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
		if markErr := MarkAsDuplicate(app, record, existing); markErr != nil {
			return markErr
		}
		result.ExactMarked++
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
	page := 1
	type groupKey struct {
		user     string
		checksum string
	}
	groups := map[groupKey][]*core.Record{}

	for {
		records, err := app.FindRecordsByFilter(
			"documents",
			"checksum != ''",
			"created",
			200,
			(page-1)*200,
			nil,
		)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			key := groupKey{user: record.GetString("user"), checksum: record.GetString("checksum")}
			groups[key] = append(groups[key], record)
		}
		if len(records) < 200 {
			break
		}
		page++
	}

	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		original := group[0]
		for _, dup := range group[1:] {
			if dup.GetString("duplicate_of") != "" {
				continue
			}
			if err := MarkAsDuplicate(app, dup, original); err != nil {
				return err
			}
			result.ExactMarked++
		}
	}
	return nil
}

func markNearDuplicates(app core.App, threshold float64, result *ScanResult) error {
	page := 1
	var docs []*core.Record
	for {
		records, err := app.FindRecordsByFilter(
			"documents",
			"ocr_text != '' && text_fingerprint != ''",
			"created",
			200,
			(page-1)*200,
			nil,
		)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			break
		}
		docs = append(docs, records...)
		if len(records) < 200 {
			break
		}
		page++
	}

	byUser := map[string][]*core.Record{}
	for _, doc := range docs {
		uid := doc.GetString("user")
		byUser[uid] = append(byUser[uid], doc)
	}

	for _, group := range byUser {
		for i := 0; i < len(group); i++ {
			a := group[i]
			if a.GetString("duplicate_of") != "" {
				continue
			}
			fa, ok := ParseFingerprintHex(a.GetString("text_fingerprint"))
			if !ok {
				continue
			}
			for j := i + 1; j < len(group); j++ {
				b := group[j]
				if b.GetString("duplicate_of") != "" {
					continue
				}
				fb, ok := ParseFingerprintHex(b.GetString("text_fingerprint"))
				if !ok {
					continue
				}
				if HammingDistance(fa, fb) > MaxHammingDistance {
					continue
				}
				if TextSimilarity(a.GetString("ocr_text"), b.GetString("ocr_text")) < threshold {
					continue
				}
				// Prefer older as original (group sorted by created ascending).
				if err := MarkAsDuplicate(app, b, a); err != nil {
					return err
				}
				result.NearMarked++
			}
		}
	}
	return nil
}
