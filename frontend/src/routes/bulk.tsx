import { useState } from 'react'
import { hasActiveFilters } from '../lib/documentQuery'
import { markDocumentsUnreviewed } from '../lib/api/documents'
import { listShareRecipients, shareDocuments } from '../lib/api/shares'
import { addTagsToDocuments, findOrCreateTag, removeTagsFromDocuments, type TagRecord } from '../lib/api/tags'
import { REPROCESS_MODE_LABELS, type ReprocessMode } from '../lib/processing'
import { tagKey } from '../lib/tagSuggestions'
import { useAsync } from '../hooks/useAsync'
import {
  useDocumentFilterOptions,
  useDocumentList,
  type DocumentList,
} from '../hooks/useDocumentList'
import { Combobox } from '../components/Combobox'
import { DocumentFilters } from '../components/DocumentFilters'
import { BulkDocumentCard } from '../components/BulkDocumentCard'
import { Pagination } from '../components/Pagination'
import { Button, selectClassName } from '../components/ui'
import { lang, t } from '../i18n'

export function BulkActionsPage() {
  const list = useDocumentList({ route: '/bulk', ownerOnly: true })
  const filterOptions = useDocumentFilterOptions()
  const { query, statusFilter, updateQuery, documents, loading, error } = list

  return (
    <section className="flex flex-col gap-3">
      <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">{t('bulk.title')}</h2>

      <div className="flex flex-col gap-4">
        <DocumentFilters
          query={query}
          search={list.search}
          onSearchChange={list.setSearch}
          updateQuery={updateQuery}
          documentTypes={filterOptions.documentTypes}
          correspondents={filterOptions.correspondents}
          tags={filterOptions.tags}
          status={statusFilter}
          owner={false}
        />

        {loading && <p className="text-sm text-ink-soft">{t('index.loading')}</p>}
        {(error || filterOptions.error) && (
          <p className="text-sm text-madder">{error || filterOptions.error}</p>
        )}

        {!loading && documents.length === 0 && (
          <div className="rounded-none border border-line bg-surface py-10 text-center">
            <p className="text-sm text-ink-soft">
              {hasActiveFilters(query) ? t('index.noMatches') : t('index.empty')}
            </p>
          </div>
        )}

        {list.message && <p className="text-sm text-forest">{list.message}</p>}

        {!loading && documents.length > 0 && (
          <>
            <BulkActionBar list={list} tags={filterOptions.tags} />
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {documents.map((document) => (
                <BulkDocumentCard
                  key={document.id}
                  document={document}
                  selected={list.selectedIds.has(document.id)}
                  onToggleSelect={list.toggleSelected}
                />
              ))}
            </div>
            <Pagination
              page={list.page}
              totalPages={list.totalPages}
              totalItems={list.totalItems}
              pageSize={list.pageSize}
              onPageSizeChange={list.changePageSize}
              onPageChange={(next) => updateQuery({ page: next })}
            />
          </>
        )}
      </div>
    </section>
  )
}

type Panel = 'tags' | 'untag' | 'share' | 'reprocess'

function TagPicker({
  options,
  value,
  onChange,
  ariaLabel,
  placeholder,
  onCreate,
}: {
  options: TagRecord[]
  value: string[]
  onChange: (next: string[]) => void
  ariaLabel: string
  placeholder: string
  onCreate?: (name: string) => void
}) {
  // The key findOrCreateTag matches on, over picked tags too.
  const taken = new Set(options.map((tag) => tagKey(tag.name)))
  const byId = new Map(options.map((tag) => [tag.id, tag]))
  const pills = value.map((id) => {
    const tag = byId.get(id)
    const name = tag?.name ?? t('bulk.unknownTag')
    return (
      <span
        key={id}
        className="flex max-w-full items-center gap-1 rounded-xs border border-line-strong bg-wash px-2 py-0.5 text-xs text-ink"
        style={{ borderColor: tag?.color || undefined }}
      >
        <span className="truncate">{name}</span>
        <button
          type="button"
          aria-label={t('bulk.removeTag', { name })}
          className="shrink-0 text-ink-faint transition-colors hover:text-madder"
          onClick={() => onChange(value.filter((other) => other !== id))}
        >
          &times;
        </button>
      </span>
    )
  })

  return (
    <Combobox
      value=""
      options={options.filter((tag) => !value.includes(tag.id)).map((tag) => ({ value: tag.id, label: tag.name }))}
      placeholder={value.length > 0 ? t('bulk.addTag') : placeholder}
      ariaLabel={ariaLabel}
      bgClassName="bg-surface"
      className="w-full min-w-0 sm:w-80"
      leading={pills.length > 0 ? pills : undefined}
      onChange={(id) => onChange([...value, id])}
      onCreate={onCreate}
      canCreate={(name) => tagKey(name) !== '' && !taken.has(tagKey(name))}
    />
  )
}

const groupClassName = 'flex flex-wrap items-center gap-2 sm:border-l sm:border-line sm:pl-4 sm:first:border-l-0 sm:first:pl-0'

function BulkActionBar({ list, tags }: { list: DocumentList; tags: TagRecord[] }) {
  const { selectedOnPage } = list
  const [panel, setPanel] = useState<Panel | null>(null)
  const [tagIds, setTagIds] = useState<string[]>([])
  const [untagIds, setUntagIds] = useState<string[]>([])
  const [createdTags, setCreatedTags] = useState<TagRecord[]>([])
  const [tagError, setTagError] = useState('')
  const [recipient, setRecipient] = useState('')
  const recipients = useAsync(listShareRecipients, [])

  const busy = list.reprocessing || list.markingReviewed || list.deleting || list.runningAction
  const none = selectedOnPage.length === 0
  // However the selection empties, the next tick must not reopen an armed panel.
  if (none && panel) setPanel(null)
  const open = panel
  const ids = selectedOnPage.map((document) => document.id)
  const reviewable = selectedOnPage.filter((document) => document.processing_status === 'needs_review')
  const reviewed = selectedOnPage.filter((document) => document.processing_status === 'completed')

  async function run(action: () => Promise<string>, fallbackError: string) {
    if (await list.runBulkAction(action, fallbackError)) setPanel(null)
  }

  function panelToggle(target: Panel, label: string, unavailable = false) {
    return (
      <Button
        variant={open === target ? 'primary' : 'secondary'}
        size="sm"
        aria-expanded={open === target}
        disabled={busy || none || unavailable}
        onClick={() => setPanel(open === target ? null : target)}
      >
        {label}
      </Button>
    )
  }

  const cancel = (
    <Button variant="secondary" size="sm" onClick={() => setPanel(null)}>
      {t('common.cancel')}
    </Button>
  )

  const allTags = [...tags, ...createdTags.filter((created) => !tags.some((tag) => tag.id === created.id))]
  const carriedTags = [
    ...new Map(selectedOnPage.flatMap((document) => document.expand?.tags ?? []).map((tag) => [tag.id, tag])).values(),
  ].sort((a, b) => a.name.localeCompare(b.name, lang))
  const untagPicked = untagIds.filter((id) => carriedTags.some((tag) => tag.id === id))
  const untagging = selectedOnPage.filter((document) =>
    (document.expand?.tags ?? []).some((tag) => untagPicked.includes(tag.id)),
  )

  async function createAndPick(name: string) {
    setTagError('')
    try {
      const tag = await findOrCreateTag(name)
      setCreatedTags((current) => [...current, tag])
      setTagIds((current) => (current.includes(tag.id) ? current : [...current, tag.id]))
    } catch (err) {
      setTagError(err instanceof Error ? err.message : t('bulk.createTagError'))
    }
  }

  return (
    <div
      role="toolbar"
      aria-label={t('bulk.toolbar')}
      className="sticky top-0 z-10 flex flex-col rounded-none border border-line bg-surface shadow-sm"
    >
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2">
        <span className="text-sm font-medium text-ink">
          {none ? t('bulk.selectPrompt') : t('bulk.selected', { count: selectedOnPage.length })}
        </span>
        <div className="flex gap-2">
          <Button variant="secondary" size="xs" onClick={list.selectAll}>
            {t('bulk.selectAll')}
          </Button>
          <Button variant="secondary" size="xs" disabled={none} onClick={list.clearSelection}>
            {t('bulk.unselectAll')}
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-3 px-4 py-3">
        <div role="group" aria-label={t('bulk.groupReview')} className={groupClassName}>
          <Button
            size="sm"
            disabled={busy || reviewable.length === 0}
            onClick={() => void list.onMarkReviewed(reviewable.map((document) => document.id))}
          >
            {t('bulk.markReviewed')}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={busy || reviewed.length === 0}
            onClick={() =>
              void run(async () => {
                await markDocumentsUnreviewed(reviewed.map((document) => document.id))
                return t('bulk.markedUnreviewed', { count: reviewed.length })
              }, t('bulk.markUnreviewedError'))
            }
          >
            {t('bulk.markUnreviewed')}
          </Button>
        </div>

        <div role="group" aria-label={t('bulk.groupOrganize')} className={groupClassName}>
          {panelToggle('tags', t('bulk.assignTagsPanel'))}
          {panelToggle('untag', t('bulk.unassignTagsPanel'), carriedTags.length === 0)}
          {panelToggle('share', t('bulk.sharePanel'))}
        </div>

        <div role="group" aria-label={t('bulk.groupProcessing')} className={groupClassName}>
          {panelToggle('reprocess', t('bulk.reprocessPanel'))}
        </div>

        <Button
          variant="danger"
          size="sm"
          className="sm:ml-auto"
          disabled={busy || none}
          onClick={() => void list.onDeleteSelected()}
        >
          {t('common.delete')}
        </Button>
      </div>

      {open && (
        <div className="flex flex-wrap items-center gap-3 border-t border-line bg-wash px-4 py-3">
          {open === 'tags' && (
            <>
              <TagPicker
                options={allTags}
                value={tagIds}
                onChange={setTagIds}
                ariaLabel={t('bulk.tagsToAssign')}
                placeholder={t('bulk.pickOrCreateTags')}
                onCreate={(name) => void createAndPick(name)}
              />
              <Button
                size="sm"
                disabled={busy || tagIds.length === 0}
                onClick={() =>
                  void run(async () => {
                    await addTagsToDocuments(ids, tagIds)
                    setTagIds([])
                    return t('bulk.tagged', { count: ids.length })
                  }, t('bulk.assignError'))
                }
              >
                {t('bulk.assign')}
              </Button>
              {cancel}
              {tagError && <p className="w-full text-sm text-madder">{tagError}</p>}
            </>
          )}

          {open === 'untag' && (
            <>
              <TagPicker
                options={carriedTags}
                value={untagPicked}
                onChange={setUntagIds}
                ariaLabel={t('bulk.tagsToUnassign')}
                placeholder={t('bulk.pickTagsToRemove')}
              />
              <Button
                size="sm"
                disabled={busy || untagging.length === 0}
                onClick={() =>
                  void run(async () => {
                    await removeTagsFromDocuments(
                      untagging.map((document) => document.id),
                      untagPicked,
                    )
                    setUntagIds([])
                    return t('bulk.untagged', { count: untagging.length })
                  }, t('bulk.unassignError'))
                }
              >
                {t('bulk.unassign')}
              </Button>
              {cancel}
            </>
          )}

          {open === 'share' && (
            <>
              <Combobox
                value={recipient}
                options={(recipients.data ?? []).map((user) => ({
                  value: user.id,
                  label: user.name ? `${user.name} (${user.email})` : user.email,
                }))}
                placeholder={recipients.data?.length === 0 ? t('bulk.noRecipients') : t('bulk.shareWithPlaceholder')}
                ariaLabel={t('bulk.shareWith')}
                bgClassName="bg-surface"
                className="w-full min-w-0 sm:w-80"
                loading={recipients.loading}
                disabled={recipients.data?.length === 0}
                onChange={setRecipient}
              />
              <Button
                size="sm"
                disabled={busy || !recipient}
                onClick={() =>
                  void run(async () => {
                    const shared = await shareDocuments(ids, recipient)
                    return shared === ids.length
                      ? t('bulk.shared', { count: shared })
                      : t('bulk.sharedSome', { count: shared, already: ids.length - shared })
                  }, t('bulk.shareError'))
                }
              >
                {t('bulk.share')}
              </Button>
              {cancel}
              {recipients.error && <p className="w-full text-sm text-madder">{recipients.error}</p>}
            </>
          )}

          {open === 'reprocess' && (
            <>
              <select
                value={list.reprocessMode}
                onChange={(event) => list.setReprocessMode(event.target.value as ReprocessMode)}
                aria-label={t('bulk.reprocessSteps')}
                className={selectClassName}
              >
                {Object.entries(REPROCESS_MODE_LABELS).map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
              <Button size="sm" disabled={busy} onClick={() => void list.onReprocessSelected()}>
                {t('activity.reprocess')}
              </Button>
              {cancel}
            </>
          )}
        </div>
      )}
    </div>
  )
}
