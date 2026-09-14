import { DOCUMENT_STATUSES, DOCUMENT_STATUS_LABELS } from '../lib/documentStatus'
import type { CorrespondentRecord, DocumentTypeRecord } from '../lib/api/documents'
import { MIN_SEARCH_LENGTH, tagIds, type DocumentQuery } from '../lib/documentQuery'
import type { TagRecord } from '../lib/api/tags'
import { FilterCombobox } from './FilterCombobox'
import { TagFilter } from './TagFilter'
import { selectClassName } from './ui'

/**
 * The status dropdown is opt-in: a list whose status the path already decides
 * must not offer one, or a reader could filter their way out of it.
 */
export function DocumentFilters({
  query,
  search,
  onSearchChange,
  updateQuery,
  documentTypes,
  correspondents,
  tags,
  status,
}: {
  query: DocumentQuery
  search: string
  onSearchChange: (value: string) => void
  updateQuery: (patch: Partial<DocumentQuery>, replace?: boolean) => void
  documentTypes: DocumentTypeRecord[]
  correspondents: CorrespondentRecord[]
  tags: TagRecord[]
  /** Omit to hide the status dropdown. */
  status?: string
}) {
  const tooShort = search.trim().length > 0 && search.trim().length < MIN_SEARCH_LENGTH
  const chosenTags = tagIds(query.tags)
  // Nothing to offer and nothing chosen is no filter at all. Decided here
  // rather than inside TagFilter because the search box shares the row and
  // has to know whether it is sharing it.
  const showTags = tags.length > 0 || chosenTags.length > 0

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
        <div className="flex w-full min-w-0 flex-col gap-1 sm:flex-1">
          <input
            type="search"
            placeholder="Search title, tags, purpose, summary..."
            value={search}
            onChange={(event) => onSearchChange(event.target.value)}
            aria-describedby={tooShort ? 'search-too-short' : undefined}
            className="w-full rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood"
          />
          {tooShort && (
            <p id="search-too-short" role="status" className="text-xs text-ink-soft">
              Type at least {MIN_SEARCH_LENGTH} characters to search.
            </p>
          )}
        </div>
        {showTags && (
          <TagFilter
            value={chosenTags}
            options={tags}
            onChange={(next) => updateQuery({ tags: next.join(',') })}
          />
        )}
        {status !== undefined && (
          <select
            value={status}
            onChange={(event) => updateQuery({ status: event.target.value })}
            aria-label="Processing status"
            className={`${selectClassName} shrink-0 sm:w-48`}
          >
            <option value="all">All statuses</option>
            {DOCUMENT_STATUSES.map((value) => (
              <option key={value} value={value}>
                {DOCUMENT_STATUS_LABELS[value]}
              </option>
            ))}
          </select>
        )}
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink-soft">From date</span>
          <input
            type="date"
            value={query.from}
            // replace: a date field fires a change per digit typed into the
            // year, and none of those belong in history. undated goes with it:
            // a range and "no date" together match nothing.
            onChange={(event) => updateQuery({ from: event.target.value, undated: false }, true)}
            className={selectClassName}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink-soft">To date</span>
          <input
            type="date"
            value={query.to}
            onChange={(event) => updateQuery({ to: event.target.value, undated: false }, true)}
            className={selectClassName}
          />
        </label>
        <FilterCombobox
          label="Document type"
          value={query.type}
          allLabel="All types"
          options={documentTypes.map((type) => ({ value: type.id, label: type.name }))}
          onChange={(next) => updateQuery({ type: next })}
        />
        <FilterCombobox
          label="Correspondent"
          value={query.correspondent}
          allLabel="All correspondents"
          options={correspondents.map((correspondent) => ({
            value: correspondent.id,
            label: correspondent.name,
          }))}
          onChange={(next) => updateQuery({ correspondent: next })}
        />
      </div>
    </div>
  )
}
