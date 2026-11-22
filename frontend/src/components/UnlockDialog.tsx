import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { t } from '../i18n'
import { unlockDocument } from '../lib/api/documents'
import { Button, inputClassName, labelClassName, labelTextClassName } from './ui'

/**
 * Takes a password_required PDF's password once. A wrong one keeps the dialog
 * open with the server's message. Mounted only while open.
 */
export function UnlockDialog({
  documentId,
  onClose,
  onUnlocked,
}: {
  documentId: string
  onClose: () => void
  onUnlocked: () => void
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    ref.current?.showModal()
  }, [])
  const close = () => ref.current?.close()

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await unlockDocument(documentId, password)
      onUnlocked()
      close()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('unlock.failed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      aria-labelledby={titleId}
      className="m-auto w-full max-w-md border border-line bg-surface p-5 text-ink backdrop:bg-ink/40"
    >
      <h2 id={titleId} className="font-display text-lg font-semibold text-ink">
        {t('unlock.title')}
      </h2>
      <form onSubmit={(event) => void submit(event)} className="mt-1 flex flex-col gap-4">
        <p className="text-sm text-ink-soft">{t('unlock.description')}</p>
        <label className={labelClassName}>
          <span className={labelTextClassName}>{t('unlock.password')}</span>
          <input
            type="password"
            autoComplete="off"
            autoFocus
            required
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            className={inputClassName}
          />
        </label>
        {error && (
          <p className="text-sm text-madder" role="alert">
            {error}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={close}>
            {t('common.cancel')}
          </Button>
          <Button type="submit" disabled={busy || password === ''}>
            {busy ? t('unlock.submitting') : t('unlock.submit')}
          </Button>
        </div>
      </form>
    </dialog>
  )
}
