import { useState } from 'react'
import type { JobOverrides } from '../lib/api/documents'
import type { TagRecord } from '../lib/api/tags'
import { REPROCESS_MODE_LABELS, type ReprocessMode } from '../lib/processing'
import { JobOverrideFields } from './BindingOverride'
import { Button, fieldHintClassName, selectClassName } from './ui'

export type BulkMode = 'reprocess' | 'review' | 'tag'

const reprocessModes: ReprocessMode[] = ['auto', 'full', 'extraction']

const hints: Record<BulkMode, string> = {
  reprocess: 'Select failed or cancelled documents to reprocess.',
  review: 'Select documents to mark reviewed.',
  tag: 'Select documents to tag.',
}

type Props = {
  mode: BulkMode
  selectedCount: number
  busy: boolean
  reprocessMode: ReprocessMode
  onReprocessModeChange: (mode: ReprocessMode) => void
  reprocessOverrides: JobOverrides
  onReprocessOverridesChange: (overrides: JobOverrides) => void
  onReprocess: () => void
  onMarkReviewed: () => void
  onSelectAll: () => void
  onClear: () => void
  /**
   * Offered alongside the mode's own action, not instead of it. Omit to hide the
   * button.
   */
  onDelete?: () => void
  /**
   * The tag controls, likewise alongside rather than instead: tagging is worth
   * doing on an Inbox pass too, not only on an unfiltered list. Omit `tags` to
   * hide both.
   */
  tags?: TagRecord[]
  onAddTag?: (tagId: string) => void
  onAssignTagsWithAI?: (tagIds: string[]) => void
}

export function DocumentBulkBar({
  mode,
  selectedCount,
  busy,
  reprocessMode,
  onReprocessModeChange,
  reprocessOverrides,
  onReprocessOverridesChange,
  onReprocess,
  onMarkReviewed,
  onSelectAll,
  onClear,
  onDelete,
  tags,
  onAddTag,
  onAssignTagsWithAI,
}: Props) {
  const [tagId, setTagId] = useState('')

  return (
    <div className="flex flex-wrap items-center gap-3 rounded-none border border-line bg-surface px-4 py-3">
      <span className="text-sm text-ink-muted">
        {selectedCount > 0
          ? `${selectedCount} selected`
          : onDelete && mode === 'review'
            ? 'Select documents to mark reviewed or delete.'
            : hints[mode]}
      </span>

      {mode === 'reprocess' ? (
        <>
          <select
            value={reprocessMode}
            onChange={(event) => onReprocessModeChange(event.target.value as ReprocessMode)}
            aria-label="Reprocess steps"
            className={selectClassName}
          >
            {reprocessModes.map((value) => (
              <option key={value} value={value}>
                {REPROCESS_MODE_LABELS[value]}
              </option>
            ))}
          </select>
          <Button disabled={busy || selectedCount === 0} onClick={onReprocess}>
            {busy ? 'Queueing...' : 'Reprocess'}
          </Button>
        </>
      ) : (
        <Button disabled={busy || selectedCount === 0} onClick={onMarkReviewed}>
          {busy ? 'Marking...' : 'Mark reviewed'}
        </Button>
      )}

      {tags && onAddTag && (
        <>
          <select
            value={tagId}
            onChange={(event) => setTagId(event.target.value)}
            aria-label="Tag to add"
            className={selectClassName}
          >
            <option value="">Tag...</option>
            {tags.map((tag) => (
              <option key={tag.id} value={tag.id}>
                {tag.name}
              </option>
            ))}
          </select>
          <Button
            variant="secondary"
            disabled={busy || selectedCount === 0 || !tagId}
            onClick={() => onAddTag(tagId)}
          >
            Add tag
          </Button>
        </>
      )}

      {tags && onAssignTagsWithAI && (
        <Button
          variant="secondary"
          disabled={busy || selectedCount === 0}
          onClick={() => onAssignTagsWithAI(tags.map((tag) => tag.id))}
        >
          {busy ? 'Working...' : 'Assign tags with AI'}
        </Button>
      )}

      {onDelete && (
        <Button variant="danger" disabled={busy || selectedCount === 0} onClick={onDelete}>
          Delete selected
        </Button>
      )}

      <Button variant="secondary" onClick={onSelectAll}>
        Select all on page
      </Button>
      {selectedCount > 0 && (
        <Button variant="secondary" onClick={onClear}>
          Clear
        </Button>
      )}
      {tags && onAssignTagsWithAI && (
        <p className={`w-full ${fieldHintClassName}`}>
          Adding a tag is free. Assigning with AI sends one request per selected document and is
          charged to your provider; it only adds tags, but reprocessing a document later discards
          what it added.
        </p>
      )}

      {mode === 'reprocess' && selectedCount > 0 && (
        <div className="w-full">
          <JobOverrideFields value={reprocessOverrides} onChange={onReprocessOverridesChange} />
        </div>
      )}
    </div>
  )
}
