import type { JobOverrides } from '../lib/api/documents'
import { REPROCESS_MODE_LABELS, type ReprocessMode } from '../lib/processing'
import { JobOverrideFields } from './BindingOverride'
import { Button, selectClassName } from './ui'

export type BulkMode = 'reprocess' | 'review'

const reprocessModes: ReprocessMode[] = ['auto', 'full', 'extraction']

const hints: Record<BulkMode, string> = {
  reprocess: 'Select failed documents to reprocess.',
  review: 'Select documents to mark reviewed.',
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
   * Offered alongside the mode's own action, not instead of it: the Inbox holds
   * documents worth throwing away as well as ones worth reviewing. Omit to hide
   * the button.
   */
  onDelete?: () => void
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
}: Props) {
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
      {mode === 'reprocess' && selectedCount > 0 && (
        <div className="w-full">
          <JobOverrideFields value={reprocessOverrides} onChange={onReprocessOverridesChange} />
        </div>
      )}
    </div>
  )
}
