import { t } from '../i18n'
import { DOCUMENT_STATUSES, DOCUMENT_STATUS_LABELS } from '../lib/documentStatus'
import type { OptionRecord } from '../lib/api/documents'
import {
  MIN_SEARCH_LENGTH,
  tagIds,
  type DocumentOwner,
  type DocumentQuery,
  type DocumentSort,
} from '../lib/documentQuery'
import type { TagRecord } from '../lib/api/tags'
import { Combobox } from './Combobox'
import { FilterCombobox } from './FilterCombobox'
import { IconSelect } from './IconSelect'
import { TagFilter } from './TagFilter'
import { dateInputClassName } from './ui'

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
  owner = true,
}: {
  query: DocumentQuery
  search: string
  onSearchChange: (value: string) => void
  updateQuery: (patch: Partial<DocumentQuery>, replace?: boolean) => void
  documentTypes: OptionRecord[]
  correspondents: OptionRecord[]
  tags: TagRecord[]
  /** Omit to hide the status dropdown. */
  status?: string
  /** False for a list that already leaves shared documents out. */
  owner?: boolean
}) {
  const tooShort = search.trim().length > 0 && search.trim().length < MIN_SEARCH_LENGTH
  const chosenTags = tagIds(query.tags)
  // Nothing to offer and nothing chosen is no filter at all. Decided here
  // rather than inside TagFilter because the search box shares the row and
  // has to know whether it is sharing it.
  const showTags = tags.length > 0 || chosenTags.length > 0 || query.untagged

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
        <div className="flex w-full min-w-0 flex-col gap-1 sm:flex-1">
          <input
            type="search"
            placeholder={t('documentFilters.searchPlaceholder')}
            value={search}
            onChange={(event) => onSearchChange(event.target.value)}
            aria-describedby={tooShort ? 'search-too-short' : undefined}
            className="w-full rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood"
          />
          {tooShort && (
            <p id="search-too-short" role="status" className="text-xs text-ink-soft">
              {t('documentFilters.tooShort', { count: MIN_SEARCH_LENGTH })}
            </p>
          )}
        </div>
        {showTags && (
          <TagFilter
            value={chosenTags}
            untagged={query.untagged}
            options={tags}
            onChange={(next, untagged) => updateQuery({ tags: next.join(','), untagged })}
          />
        )}
        {owner && (
          <OwnerMenu query={query} updateQuery={updateQuery} className="hidden sm:block" />
        )}
        {status !== undefined && (
          <Combobox
            value={status}
            options={[
              { value: 'all', label: t('documentFilters.allStatuses') },
              ...DOCUMENT_STATUSES.map((value) => ({ value, label: DOCUMENT_STATUS_LABELS[value] })),
            ]}
            placeholder={t('documentFilters.allStatuses')}
            ariaLabel={t('documentFilters.status')}
            bgClassName="bg-surface"
            className="shrink-0 sm:w-48"
            onChange={(next) => updateQuery({ status: next })}
          />
        )}
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,0.8fr)_minmax(0,0.8fr)_minmax(0,1.3fr)_minmax(0,1.3fr)]">
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink-soft">{t('documentFilters.from')}</span>
          <input
            type="date"
            value={query.from}
            // replace: a date field fires a change per digit typed into the
            // year, and none of those belong in history. undated goes with it:
            // a range and "no date" together match nothing.
            onChange={(event) => updateQuery({ from: event.target.value, undated: false }, true)}
            className={dateInputClassName}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink-soft">{t('documentFilters.to')}</span>
          <input
            type="date"
            value={query.to}
            onChange={(event) => updateQuery({ to: event.target.value, undated: false }, true)}
            className={dateInputClassName}
          />
        </label>
        <FilterCombobox
          label={t('documentFilters.type')}
          value={query.type}
          allLabel={t('documentFilters.allTypes')}
          options={documentTypes.map((type) => ({ value: type.id, label: type.name }))}
          onChange={(next) => updateQuery({ type: next })}
        />
        <FilterCombobox
          label={t('documentFilters.correspondent')}
          value={query.correspondent}
          allLabel={t('documentFilters.allCorrespondents')}
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

const SORT_LABELS: Record<DocumentSort, () => string> = {
  '-created': () => t('documentFilters.sortAddedDesc'),
  created: () => t('documentFilters.sortAddedAsc'),
  '-document_date': () => t('documentFilters.sortDateDesc'),
  document_date: () => t('documentFilters.sortDateAsc'),
  title: () => t('documentFilters.sortTitleAsc'),
  '-title': () => t('documentFilters.sortTitleDesc'),
}

/** A search is ranked by relevance, so the menu stands down while one is active. */
export function DocumentSortMenu({
  query,
  updateQuery,
}: {
  query: DocumentQuery
  updateQuery: (patch: Partial<DocumentQuery>) => void
}) {
  const searching = query.q !== ''
  return (
    <IconSelect
      label={t('documentFilters.sort')}
      icon="M7 4v12M4 13l3 3 3-3M13 16V4M10 7l3-3 3 3"
      value={searching ? 'relevance' : query.sort}
      disabled={searching}
      options={
        searching
          ? [{ value: 'relevance', label: t('documentFilters.sortRelevance') }]
          : Object.entries(SORT_LABELS).map(([value, label]) => ({ value, label: label() }))
      }
      onChange={(next) => updateQuery({ sort: next as DocumentSort })}
    />
  )
}

/**
 * On a phone the filter row stacks, where one icon would take a whole line, so
 * the page shows a second copy in its header there and this one hides.
 */
export function OwnerMenu({
  query,
  updateQuery,
  className,
}: {
  query: DocumentQuery
  updateQuery: (patch: Partial<DocumentQuery>) => void
  className: string
}) {
  return (
    <IconSelect
      label={t('documentFilters.owner')}
      icon="M10 9.5a3 3 0 1 0 0-6 3 3 0 0 0 0 6ZM4.5 16.5a5.5 5.5 0 0 1 11 0"
      value={query.owner}
      options={[
        { value: 'all', label: t('documentFilters.allOwners') },
        { value: 'mine', label: t('documentFilters.mine') },
        { value: 'shared', label: t('documentFilters.shared') },
      ]}
      className={className}
      onChange={(next) => updateQuery({ owner: next as DocumentOwner })}
    />
  )
}
