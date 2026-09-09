import { DOCUMENT_STATUSES, DOCUMENT_STATUS_LABELS } from '../lib/documentStatus'
import type { CorrespondentRecord, DocumentTypeRecord } from '../lib/api/documents'
import type { DocumentQuery } from '../lib/documentQuery'
import { FilterCombobox } from './FilterCombobox'
import { selectClassName } from './ui'

/**
 * The search box and the filters that compose with it.
 *
 * The status dropdown is opt-in: a list whose status the path already decides
 * must not offer one, or a reader could filter their way out of the list they
 * are on.
 */
export function DocumentFilters({
  query,
  search,
  onSearchChange,
  updateQuery,
  documentTypes,
  correspondents,
  status,
}: {
  query: DocumentQuery
  search: string
  onSearchChange: (value: string) => void
  updateQuery: (patch: Partial<DocumentQuery>, replace?: boolean) => void
  documentTypes: DocumentTypeRecord[]
  correspondents: CorrespondentRecord[]
  /** Omit to hide the status dropdown. */
  status?: string
}) {
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-3 sm:flex-row">
        <input
          type="search"
          placeholder="Search title, tags, purpose, summary..."
          value={search}
          onChange={(event) => onSearchChange(event.target.value)}
          className="w-full rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood"
        />
        {status !== undefined && (
          <select
            value={status}
            onChange={(event) => updateQuery({ status: event.target.value })}
            aria-label="Processing status"
            className={`${selectClassName} sm:w-48`}
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
            // year, and none of those belong in history.
            onChange={(event) => updateQuery({ from: event.target.value }, true)}
            className={selectClassName}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink-soft">To date</span>
          <input
            type="date"
            value={query.to}
            onChange={(event) => updateQuery({ to: event.target.value }, true)}
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
