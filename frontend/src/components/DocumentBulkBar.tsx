import { t } from '../i18n'
import type { JobOverrides } from '../lib/api/documents'
import { REPROCESS_MODE_LABELS, type ReprocessMode } from '../lib/processing'
import { JobOverrideFields } from './BindingOverride'
import { Button, selectClassName } from './ui'

export type BulkMode = 'reprocess' | 'review'

const reprocessModes: ReprocessMode[] = ['auto', 'full', 'extraction']

const hints: Record<BulkMode, string> = {
  reprocess: t('documentBulkBar.hintReprocess'),
  review: t('documentBulkBar.hintReview'),
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
          ? t('documentBulkBar.selected', { count: selectedCount })
          : onDelete && mode === 'review'
            ? t('documentBulkBar.hintReviewOrDelete')
            : hints[mode]}
      </span>

      {mode === 'reprocess' ? (
        <>
          <select
            value={reprocessMode}
            onChange={(event) => onReprocessModeChange(event.target.value as ReprocessMode)}
            aria-label={t('documentBulkBar.reprocessSteps')}
            className={selectClassName}
          >
            {reprocessModes.map((value) => (
              <option key={value} value={value}>
                {REPROCESS_MODE_LABELS[value]}
              </option>
            ))}
          </select>
          <Button disabled={busy || selectedCount === 0} onClick={onReprocess}>
            {busy ? t('documentBulkBar.queueing') : t('documentBulkBar.reprocess')}
          </Button>
        </>
      ) : (
        <Button disabled={busy || selectedCount === 0} onClick={onMarkReviewed}>
          {busy ? t('documentCard.marking') : t('documentCard.markReviewed')}
        </Button>
      )}

      {onDelete && (
        <Button variant="danger" disabled={busy || selectedCount === 0} onClick={onDelete}>
          {t('documentBulkBar.deleteSelected')}
        </Button>
      )}

      <Button variant="secondary" onClick={onSelectAll}>
        {t('documentBulkBar.selectAll')}
      </Button>
      {selectedCount > 0 && (
        <Button variant="secondary" onClick={onClear}>
          {t('documentBulkBar.clear')}
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
