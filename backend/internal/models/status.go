package models

const (
	JobStatusPending     = "pending"
	JobStatusRunning     = "running"
	JobStatusCompleted   = "completed"
	JobStatusFailed      = "failed"
	JobStatusNeedsReview = "needs_review"

	DocStatusPending     = "pending"
	DocStatusProcessing  = "processing"
	DocStatusCompleted   = "completed"
	DocStatusFailed      = "failed"
	DocStatusNeedsReview = "needs_review"

	MetadataSourceUser = "user"
)

// StatusFilterUnfinished is not a document status: it is what the Inbox asks
// for, meaning every status except completed. The list and the search endpoint
// both accept it where a status goes, so the two agree about what the Inbox
// holds. Mirrored in frontend/src/lib/documentStatus.ts.
const StatusFilterUnfinished = "unfinished"

// UnfinishedDocStatuses expands that filter. Spelled as the set rather than as
// "not completed" because the search index matches terms, and a negation there
// would also match documents with no status at all.
var UnfinishedDocStatuses = []string{
	DocStatusPending,
	DocStatusProcessing,
	DocStatusFailed,
	DocStatusNeedsReview,
}
