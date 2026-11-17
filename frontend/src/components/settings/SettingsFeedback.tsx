import { t } from '../../i18n'
import { Button, sectionClassName } from '../ui'

/**
 * The save button and, beside it, what the last save did. aria-disabled rather
 * than disabled while saving: a disabled button drops keyboard focus to <body>.
 */
export function SaveSettingsButton({
  saving,
  dirty,
  error,
  success,
}: {
  saving: boolean
  dirty: boolean
  error: string
  success: string
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <Button
        type="submit"
        aria-disabled={saving}
        className="aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
      >
        {saving ? t('settingsFeedback.saving') : t('settingsFeedback.save')}
      </Button>
      <ResultNotice
        error={error}
        success={success || (dirty && !saving ? t('settingsFeedback.unsaved') : '')}
        muted={!success}
      />
    </div>
  )
}

/** Always in the tree, so a screen reader hears the text arrive. */
export function ResultNotice({
  error,
  success,
  muted = false,
  className = '',
}: {
  error: string
  success: string
  muted?: boolean
  className?: string
}) {
  return (
    <p
      role="status"
      className={`text-sm ${error ? 'text-madder' : muted ? 'text-ink-soft' : 'text-forest'} ${className}`}
    >
      {error || success}
    </p>
  )
}

/** What a tab shows while the settings record is still on its way. */
export function SettingsLoading({ error }: { error: string }) {
  return <p className="text-sm text-ink-soft">{error || t('settingsFeedback.loading')}</p>
}

/**
 * What the pages a managed instance leaves out of its navigation show: this is
 * for a link or a bookmark.
 */
export function ManagedByHostNotice() {
  return (
    <section className={sectionClassName}>
      <p className="text-sm text-ink-soft">{t('settingsFeedback.managedByHost')}</p>
    </section>
  )
}
