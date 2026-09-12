import { useEffect, useRef } from 'react'

import { Button, sectionClassName } from '../ui'

export function SaveSettingsButton({ saving }: { saving: boolean }) {
  return (
    <div>
      <Button type="submit" disabled={saving}>
        {saving ? 'Saving...' : 'Save settings'}
      </Button>
    </div>
  )
}

/**
 * Save feedback as a modal: the inline lines at the foot of the page these
 * fields used to share were read by nobody. Success clears itself, an error
 * waits to be dismissed.
 */
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
          <Button onClick={onClose}>Close</Button>
        </div>
      )}
    </dialog>
  )
}

/** What a tab shows while the settings record is still on its way. */
export function SettingsLoading({ error }: { error: string }) {
  return <p className="text-sm text-ink-soft">{error || 'Loading settings...'}</p>
}

/**
 * What the operator-owned tabs show on a managed instance. They are left out of
 * the tab bar there, so this is for a link or a bookmark: the page says why it
 * is empty rather than bouncing somewhere else.
 */
export function ManagedByHostNotice() {
  return (
    <section className={sectionClassName}>
      <p className="text-sm text-ink-soft">
        These settings are managed by your hosting provider. Providers, models and duplicate
        detection come from the instance&rsquo;s environment and cannot be changed here.
      </p>
    </section>
  )
}
