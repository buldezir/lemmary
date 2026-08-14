package ngxapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

func handleListTasks(e *core.RequestEvent) error {
	filter := "document.user = {:userId}"
	_, params := ownerScope(e.Auth.Id)

	if taskID := strings.TrimSpace(e.Request.URL.Query().Get("task_id")); taskID != "" {
		filter += " && task_id = {:taskId}"
		params["taskId"] = taskID
	}

	records, err := e.App.FindRecordsByFilter(
		"processing_jobs",
		filter,
		"-created",
		100,
		0,
		params,
	)
	if err != nil {
		return internalError(e, err)
	}

	results := make([]any, 0, len(records))
	for _, job := range records {
		results = append(results, mapTask(e.App, job))
	}

	return writeJSON(e, http.StatusOK, results)
}
