import { useEffect, useRef } from 'react'

import { t } from '../../i18n'
import { Button, sectionClassName } from '../ui'

export function SaveSettingsButton({ saving }: { saving: boolean }) {
  return (
    <div>
      <Button type="submit" disabled={saving}>
        {saving ? t('settingsFeedback.saving') : t('settingsFeedback.save')}
      </Button>
    </div>
  )
}

/** Success clears itself, an error waits to be dismissed. */
export function ResultDialog({
  error,
  success,
  onClose,
}: {
  error: string
  success: string
  onClose: () => void
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const open = Boolean(error || success)

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (!open) {
      dialog.close()
      return
    }
    dialog.showModal()
    if (error) return
    const timer = setTimeout(onClose, 1500)
    return () => clearTimeout(timer)
  }, [open, error, onClose])

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      className="m-auto max-w-sm border border-line bg-surface p-5 text-ink backdrop:bg-ink/40"
    >
      <p className={`text-sm ${error ? 'text-madder' : 'text-forest'}`}>{error || success}</p>
      {error && (
        <div className="mt-4 flex justify-end">
          <Button onClick={onClose}>{t('common.close')}</Button>
        </div>
      )}
    </dialog>
  )
}

/** What a tab shows while the settings record is still on its way. */
export function SettingsLoading({ error }: { error: string }) {
  return <p className="text-sm text-ink-soft">{error || t('settingsFeedback.loading')}</p>
}

/**
 * What the operator-owned tabs show on a managed instance, where they are left
 * out of the tab bar: this is for a link or a bookmark.
 */
export function ManagedByHostNotice() {
  return (
    <section className={sectionClassName}>
      <p className="text-sm text-ink-soft">{t('settingsFeedback.managedByHost')}</p>
    </section>
  )
}
