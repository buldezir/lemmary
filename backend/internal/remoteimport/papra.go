package remoteimport

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/worker"
)

const papraPageSize = 100

type papraOrganization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type papraTag struct {
	Name string `json:"name"`
}

type papraDocument struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	OriginalName string     `json:"originalName"`
	Content      string     `json:"content"`
	DocumentDate string     `json:"documentDate"`
	Tags         []papraTag `json:"tags"`
}

// newPapraClient takes a Papra API key with organizations:read and documents:read.
func newPapraClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	client, err := NewClient(baseURL, apiKey, httpClient)
	if err != nil {
		return nil, err
	}
	client.authorization = "Bearer " + strings.TrimSpace(apiKey)
	return client, nil
}

func (c *Client) papraOrganizations() ([]papraOrganization, error) {
	var body struct {
		Organizations []papraOrganization `json:"organizations"`
	}
	err := c.getJSON("/api/organizations", &body)
	return body.Organizations, err
}

func (c *Client) forEachPapraDocuments(orgID string, fn func([]papraDocument) error) error {
	for pageIndex := 0; ; pageIndex++ {
		if pageIndex >= maxListPages {
			return fmt.Errorf("remote returned more than %d pages for organization %s; aborting", maxListPages, orgID)
		}
		var body struct {
			Documents []papraDocument `json:"documents"`
		}
		rel := fmt.Sprintf("/api/organizations/%s/documents?pageIndex=%d&pageSize=%d", url.PathEscape(orgID), pageIndex, papraPageSize)
		if err := c.getJSON(rel, &body); err != nil {
			return err
		}
		if err := fn(body.Documents); err != nil {
			return err
		}
		if len(body.Documents) < papraPageSize {
			return nil
		}
	}
}

func papraDocumentPath(orgID, docID string) string {
	return fmt.Sprintf("/api/organizations/%s/documents/%s", url.PathEscape(orgID), url.PathEscape(docID))
}

// papraDocument fetches one document in full: the list leaves its text out.
func (c *Client) papraDocument(orgID, docID string) (papraDocument, error) {
	var body struct {
		Document papraDocument `json:"document"`
	}
	err := c.getJSON(papraDocumentPath(orgID, docID), &body)
	return body.Document, err
}

func (c *Client) downloadPapraDocument(orgID, docID string) (downloadedFile, error) {
	return c.download(papraDocumentPath(orgID, docID)+"/file", docID+".bin")
}

// StartPapra imports every organization the key can read, in the background.
// It shares the one-import-per-owner guard with the Paperless-ngx import.
func StartPapra(app core.App, ownerUserID, baseURL, apiKey, mode string) (string, error) {
	return registry.Start(ownerUserID, func(func(done, total int)) (Result, error) {
		client, err := newPapraClient(baseURL, apiKey, nil)
		if err != nil {
			return Result{}, err
		}
		return runPapraImport(app, ownerUserID, mode, client)
	})
}

func runPapraImport(app core.App, ownerUserID, mode string, client *Client) (Result, error) {
	parsedMode, err := ParseMode(mode)
	if err != nil {
		return Result{}, err
	}
	orgs, err := client.papraOrganizations()
	if err != nil {
		return Result{}, fmt.Errorf("list organizations: %w", err)
	}

	result := Result{Errors: []string{}}
	tags := map[string]string{}
	for _, org := range orgs {
		err := client.forEachPapraDocuments(org.ID, func(docs []papraDocument) error {
			for _, doc := range docs {
				err := importPapraDocument(app, client, ownerUserID, parsedMode, org.ID, doc, tags, &result)
				result.count(fmt.Sprintf("document %s (%s)", doc.ID, strings.TrimSpace(doc.Name)), err)
			}
			return nil
		})
		// A key limited to some organizations still lists all of the user's.
		if err != nil {
			appendError(&result, fmt.Sprintf("organization %s: %v", strings.TrimSpace(org.Name), err))
		}
	}
	return result, nil
}

func importPapraDocument(app core.App, client *Client, ownerUserID, mode, orgID string, doc papraDocument, tags map[string]string, result *Result) error {
	file, err := client.downloadPapraDocument(orgID, doc.ID)
	if err != nil {
		return err
	}
	filename := pathBase(doc.OriginalName)
	if filename == "" {
		filename = pathBase(doc.Name)
	}
	if filename == "" {
		filename = file.Name
	}
	if mode != ModePreserve {
		return saveDocument(app, ownerUserID, filename, file.Data, nil)
	}

	full, err := client.papraDocument(orgID, doc.ID)
	if err != nil {
		return err
	}
	tagIDs, err := ensurePapraTags(app, ownerUserID, full.Tags, tags, &result.TagsUpserted)
	if err != nil {
		return err
	}
	return saveDocument(app, ownerUserID, filename, file.Data, func(record *core.Record) []models.FieldWrite {
		if title := strings.TrimSpace(strings.TrimSuffix(full.Name, path.Ext(filename))); title != "" {
			record.Set("title", title)
		}
		if ocr := strings.TrimSpace(full.Content); ocr != "" {
			record.Set("ocr_text", ocr)
		}
		if date := papraDate(full.DocumentDate); date != "" {
			record.Set("document_date", date)
		}
		if len(tagIDs) > 0 {
			record.Set("tags", tagIDs)
		}
		return nil
	})
}

func ensurePapraTags(app core.App, ownerUserID string, remote []papraTag, cache map[string]string, created *int) ([]string, error) {
	ids := make([]string, 0, len(remote))
	for _, tag := range remote {
		id, ok := cache[tag.Name]
		if !ok {
			var isNew bool
			var err error
			id, isNew, err = worker.EnsureTag(app, ownerUserID, tag.Name)
			if err != nil {
				return nil, fmt.Errorf("tag %q: %w", tag.Name, err)
			}
			cache[tag.Name] = id
			if isNew {
				*created++
			}
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// papraDate rounds to the nearest UTC day: a date picked in Papra's browser UI
// is a local midnight, which in UTC can fall on the day before.
func papraDate(raw string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return t.UTC().Add(12 * time.Hour).Format("2006-01-02")
}
