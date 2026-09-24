import { useCallback, useEffect, useRef, useState } from 'react'
import {
  listDocumentShares,
  listShareRecipients,
  revokeDocumentShare,
  shareDocument,
  type DirectoryUser,
  type ShareRecord,
} from '../lib/api/shares'
import { Combobox } from './Combobox'
import { Button } from './ui'

function label(user: Pick<DirectoryUser, 'email' | 'name'>): string {
  return user.name ? `${user.name} (${user.email})` : user.email
}

/**
 * Read-only sharing for one document. Everything here is the owner's: a reader
 * never sees this, because they cannot grant or revoke anything.
 */
export function ShareDialog({
  documentId,
  open,
  onClose,
}: {
  documentId: string
  open: boolean
  onClose: () => void
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const [users, setUsers] = useState<DirectoryUser[]>([])
  const [shares, setShares] = useState<ShareRecord[]>([])
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const reload = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [accounts, current] = await Promise.all([listShareRecipients(), listDocumentShares(documentId)])
      setUsers(accounts)
      setShares(current)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load sharing.')
    } finally {
      setLoading(false)
    }
  }, [documentId])

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (!open) {
      dialog.close()
      return
    }
    dialog.showModal()
    let cancelled = false
    void Promise.resolve().then(() => {
      if (!cancelled) void reload()
    })
    return () => {
      cancelled = true
    }
  }, [open, reload])

  const sharedWith = new Set(shares.map((share) => share.user))
  const available = users.filter((user) => !sharedWith.has(user.id))
  const byID = new Map(users.map((user) => [user.id, user]))

  async function run(action: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await action()
      await reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to change sharing.')
      setBusy(false)
      return
    }
    setBusy(false)
  }

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      className="m-auto w-full max-w-md border border-line bg-surface p-5 text-ink backdrop:bg-ink/40"
    >
      <h2 className="font-display text-lg font-semibold text-ink">Share</h2>
      <p className="mt-1 text-sm text-ink-soft">
        Anyone you add can read this document and open its file. They cannot change or delete it.
      </p>

      {loading && <p className="mt-4 text-sm text-ink-soft">Loading accounts...</p>}
      {error && <p className="mt-4 text-sm text-madder">{error}</p>}

      {!loading && (
        <div className="mt-4 flex flex-col gap-3">
          <Combobox
            value=""
            options={available.map((user) => ({ value: user.id, label: label(user) }))}
            placeholder={available.length > 0 ? 'Add an account...' : 'No other accounts'}
            ariaLabel="Share with"
            bgClassName="bg-surface"
            disabled={busy || available.length === 0}
            onChange={(userId) => void run(() => shareDocument(documentId, userId))}
          />

          {shares.length === 0 ? (
            <p className="text-sm text-ink-soft">Not shared with anyone.</p>
          ) : (
            <ul className="flex flex-col gap-1">
              {shares.map((share) => {
                const who = byID.get(share.user)
                return (
                  <li
                    key={share.id}
                    className="flex items-center justify-between gap-2 border border-line px-2 py-1.5 text-sm"
                  >
                    <span className="truncate">{who ? label(who) : 'Unknown account'}</span>
                    <Button
                      variant="secondary"
                      size="xs"
                      disabled={busy}
                      onClick={() => void run(() => revokeDocumentShare(share.id))}
                    >
                      Remove
                    </Button>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      )}

      <div className="mt-5 flex justify-end">
        <Button onClick={onClose}>Done</Button>
      </div>
    </dialog>
  )
}
