package fulltext

import (
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/mapping"
	index "github.com/blevesearch/bleve_index_api"
)

const (
	// MappingVersion is bumped when the Bleve mapping changes so Open wipes and rebuilds.
	MappingVersion = "2"

	AnalyzerName = "archive"

	FieldUser              = "user"
	FieldProcessingStatus  = "processing_status"
	FieldDocumentType      = "document_type"
	FieldCorrespondent     = "correspondent"
	FieldTags              = "tags"
	FieldDocumentDate      = "document_date"
	FieldTitle             = "title"
	FieldTitleOriginal     = "title_original"
	FieldPurpose           = "purpose"
	FieldPurposeOriginal   = "purpose_original"
	FieldSummary           = "summary"
	FieldSummaryOriginal   = "summary_original"
	FieldOCRText           = "ocr_text"
	FieldTagNames          = "tag_names"
	FieldDocumentTypeName  = "document_type_name"
	FieldCorrespondentName = "correspondent_name"
	FieldPeople            = "people_or_organizations"
	FieldAll               = "all"
)

func newMapping() (mapping.IndexMapping, error) {
	im := bleve.NewIndexMapping()
	im.DefaultAnalyzer = AnalyzerName
	im.DefaultField = FieldAll
	im.IndexDynamic = false
	im.StoreDynamic = false
	im.DocValuesDynamic = false
	im.ScoringModel = index.BM25Scoring

	if err := im.AddCustomAnalyzer(AnalyzerName, map[string]any{
		"type":          custom.Name,
		"tokenizer":     unicode.Name,
		"token_filters": []string{lowercase.Name},
	}); err != nil {
		return nil, err
	}

	doc := bleve.NewDocumentMapping()
	doc.Dynamic = false

	doc.AddFieldMappingsAt(FieldUser, keywordField())
	doc.AddFieldMappingsAt(FieldProcessingStatus, keywordField())
	doc.AddFieldMappingsAt(FieldDocumentType, keywordField())
	doc.AddFieldMappingsAt(FieldCorrespondent, keywordField())
	doc.AddFieldMappingsAt(FieldTags, keywordField())
	doc.AddFieldMappingsAt(FieldDocumentDate, dateField())

	doc.AddFieldMappingsAt(FieldTitle, textField(false, true))
	doc.AddFieldMappingsAt(FieldTitleOriginal, textField(false, true))
	doc.AddFieldMappingsAt(FieldPurpose, textField(false, true))
	doc.AddFieldMappingsAt(FieldPurposeOriginal, textField(false, true))
	doc.AddFieldMappingsAt(FieldSummary, textField(false, true))
	doc.AddFieldMappingsAt(FieldSummaryOriginal, textField(false, true))
	doc.AddFieldMappingsAt(FieldOCRText, textField(true, true))
	doc.AddFieldMappingsAt(FieldTagNames, textField(false, true))
	doc.AddFieldMappingsAt(FieldDocumentTypeName, textField(false, true))
	doc.AddFieldMappingsAt(FieldCorrespondentName, textField(false, true))
	doc.AddFieldMappingsAt(FieldPeople, textField(false, true))
	// FieldAll is only the query-string DefaultField fallback; it is never
	// highlighted, so term vectors would just double posting storage.
	doc.AddFieldMappingsAt(FieldAll, textField(false, false))

	im.DefaultMapping = doc
	return im, nil
}

func keywordField() *mapping.FieldMapping {
	fm := bleve.NewKeywordFieldMapping()
	fm.IncludeInAll = false
	fm.IncludeTermVectors = false
	fm.Store = false
	return fm
}

func textField(store, termVectors bool) *mapping.FieldMapping {
	fm := bleve.NewTextFieldMapping()
	fm.Analyzer = AnalyzerName
	fm.IncludeInAll = false
	fm.Store = store
	fm.IncludeTermVectors = termVectors
	return fm
}

func dateField() *mapping.FieldMapping {
	fm := bleve.NewDateTimeFieldMapping()
	fm.IncludeInAll = false
	fm.Store = false
	return fm
}
