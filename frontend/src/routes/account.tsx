import { type SubmitEvent, useState } from 'react'
import { getMe } from '../lib/auth'
import {
  deletePasskey,
  listPasskeys,
  passkeyDateLabel,
  registerPasskey,
  renamePasskey,
  type Passkey,
} from '../lib/api/passkeys'
import { defaultPasskeyName, passkeysSupported, passkeyUnavailableHint } from '../lib/webauthn'
import { useAsync } from '../hooks/useAsync'
import {
  Button,
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'

function SignedInSection() {
  const { data: me } = useAsync(getMe, [])

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>Signed in as</h2>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm text-ink">{me?.email || '...'}</span>
        {me?.is_admin && (
          <span className="border border-line-strong px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.12em] text-ink-soft">
            Admin
          </span>
        )}
      </div>
    </section>
  )
}

type PasskeyRowProps = {
  passkey: Passkey
  busy: boolean
  onRename: (id: string, name: string) => Promise<void>
  onDelete: (passkey: Passkey) => Promise<void>
}

function PasskeyRow({ passkey, busy, onRename, onDelete }: PasskeyRowProps) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(passkey.name)

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = draft.trim()
    if (!name) {
      return
    }
    await onRename(passkey.id, name)
    setEditing(false)
  }

  return (
    <li className="flex flex-wrap items-center justify-between gap-2 rounded-xs border border-line bg-bright px-3 py-2">
      {editing ? (
        <form className="flex flex-1 flex-wrap items-center gap-2" onSubmit={onSubmit}>
          <input
            aria-label="Passkey name"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            className={`${inputClassName} max-w-xs flex-1`}
          />
          <Button type="submit" size="xs" disabled={busy}>
            Save
          </Button>
          <Button
            size="xs"
            variant="secondary"
            disabled={busy}
            onClick={() => {
              setDraft(passkey.name)
              setEditing(false)
            }}
          >
            Cancel
          </Button>
        </form>
      ) : (
        <>
          <div className="min-w-0">
            <p className="truncate text-sm font-medium text-ink">{passkey.name}</p>
            <p className="text-xs text-ink-soft">
              Added {passkeyDateLabel(passkey.created)} · Last used{' '}
              {passkeyDateLabel(passkey.last_used)}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="xs"
              variant="secondary"
              disabled={busy}
              onClick={() => {
                setDraft(passkey.name)
                setEditing(true)
              }}
            >
              Rename
            </Button>
            <Button
              size="xs"
              variant="danger"
              disabled={busy}
              onClick={() => void onDelete(passkey)}
            >
              Delete
            </Button>
          </div>
        </>
      )}
    </li>
  )
}

function PasskeysSection() {
  const { data: passkeys, error: loadError, reload } = useAsync(listPasskeys, [])
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  // Only enrolling needs a working ceremony. Listing and deleting must keep
  // working on an address that cannot run one, or somebody who enrolled over
  // HTTPS and later opened the app over plain HTTP on the LAN could not clean up.
  const canEnroll = passkeysSupported()
  const rows = passkeys ?? []

  function openAddForm() {
    setName(defaultPasskeyName())
    setError('')
    setNotice('')
    setAdding(true)
  }

  async function onAdd(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setBusy(true)
      setError('')
      setNotice('')
      const created = await registerPasskey(name.trim() || defaultPasskeyName())
      await reload()
      setAdding(false)
      setNotice(`Added "${created.name}".`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add the passkey')
    } finally {
      setBusy(false)
    }
  }

  async function onRename(id: string, nextName: string) {
    try {
      setBusy(true)
      setError('')
      setNotice('')
      await renamePasskey(id, nextName)
      await reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to rename the passkey')
    } finally {
      setBusy(false)
    }
  }

  async function onDelete(passkey: Passkey) {
    const lastOne = rows.length === 1
    const warning = lastOne
      ? `Remove "${passkey.name}"? This is your last passkey — you will need your email and password (or a sign-in provider) after this.`
      : `Remove "${passkey.name}"?`
    if (!window.confirm(warning)) {
      return
    }
    try {
      setBusy(true)
      setError('')
      setNotice('')
      await deletePasskey(passkey.id)
      await reload()
      setNotice(`Removed "${passkey.name}".`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to remove the passkey')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>Passkeys</h2>
      <p className={`${fieldHintClassName} mb-4`}>
        Sign in with your fingerprint, face, or device PIN instead of a password. Add one per device
        so losing a phone does not lock you out.
      </p>

      {loadError && <p className="mb-3 text-sm text-madder">{loadError}</p>}
      {error && <p className="mb-3 text-sm text-madder">{error}</p>}
      {notice && <p className="mb-3 text-sm text-ink-soft">{notice}</p>}

      {rows.length === 0 ? (
        <p className="mb-4 text-sm text-ink-soft">No passkeys yet.</p>
      ) : (
        <ul className="mb-4 flex flex-col gap-2">
          {rows.map((passkey) => (
            <PasskeyRow
              key={passkey.id}
              passkey={passkey}
              busy={busy}
              onRename={onRename}
              onDelete={onDelete}
            />
          ))}
        </ul>
      )}

      {!canEnroll && <p className="text-sm text-amber-800">{passkeyUnavailableHint()}</p>}

      {canEnroll && !adding && (
        <Button onClick={openAddForm} disabled={busy}>
          Add a passkey
        </Button>
      )}

      {canEnroll && adding && (
        <form className="flex flex-col gap-3" onSubmit={onAdd}>
          <label className={labelClassName}>
            <span className={labelTextClassName}>Name</span>
            <input
              value={name}
              onChange={(event) => setName(event.target.value)}
              className={`${inputClassName} max-w-sm`}
            />
          </label>
          <p className={fieldHintClassName}>Something you will recognize, like the device name.</p>
          <div className="flex items-center gap-2">
            <Button type="submit" disabled={busy}>
              {busy ? 'Waiting for your device...' : 'Create passkey'}
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => setAdding(false)}>
              Cancel
            </Button>
          </div>
        </form>
      )}
    </section>
  )
}

export function AccountPage() {
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-6">
      <div>
        <h1 className="font-display text-2xl font-semibold tracking-tight text-ink">Account</h1>
        <p className="mt-1 text-sm text-ink-soft">How you sign in to this archive.</p>
      </div>
      <SignedInSection />
      <PasskeysSection />
    </div>
  )
}
