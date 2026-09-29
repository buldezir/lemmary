import { type SubmitEvent, useState } from 'react'
import {
  createManagedUser,
  deleteManagedUser,
  listManagedUsers,
  updateManagedUser,
  type ManagedUser,
  type ManagedUserInput,
} from '../lib/api/users'
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
import { t } from '../i18n'

type Draft = { email: string; name: string; password: string }

const emptyDraft: Draft = { email: '', name: '', password: '' }

function UserFields({
  draft,
  onChange,
  passwordLabel,
}: {
  draft: Draft
  onChange: (next: Draft) => void
  passwordLabel: string
}) {
  return (
    <div className="grid gap-3 sm:grid-cols-3">
      <label className={labelClassName}>
        <span className={labelTextClassName}>{t('managementUsers.email')}</span>
        <input
          type="email"
          required
          autoComplete="off"
          value={draft.email}
          onChange={(event) => onChange({ ...draft, email: event.target.value })}
          className={inputClassName}
        />
      </label>
      <label className={labelClassName}>
        <span className={labelTextClassName}>{t('managementUsers.name')}</span>
        <input
          autoComplete="off"
          value={draft.name}
          onChange={(event) => onChange({ ...draft, name: event.target.value })}
          className={inputClassName}
        />
      </label>
      <label className={labelClassName}>
        <span className={labelTextClassName}>{passwordLabel}</span>
        <input
          type="password"
          autoComplete="new-password"
          minLength={8}
          value={draft.password}
          onChange={(event) => onChange({ ...draft, password: event.target.value })}
          className={inputClassName}
        />
      </label>
    </div>
  )
}

function UserRow({
  user,
  busy,
  onSave,
  onDelete,
}: {
  user: ManagedUser
  busy: boolean
  onSave: (user: ManagedUser, input: ManagedUserInput) => Promise<boolean>
  onDelete: (user: ManagedUser) => Promise<void>
}) {
  const [draft, setDraft] = useState<Draft | null>(null)

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!draft) return
    const input: ManagedUserInput = { email: draft.email.trim(), name: draft.name.trim() }
    if (draft.password) input.password = draft.password
    if (await onSave(user, input)) setDraft(null)
  }

  if (draft) {
    return (
      <li className="rounded-xs border border-line bg-bright px-3 py-3">
        <form className="flex flex-col gap-3" onSubmit={onSubmit}>
          <UserFields draft={draft} onChange={setDraft} passwordLabel={t('managementUsers.newPassword')} />
          <p className={fieldHintClassName}>{t('managementUsers.keepPassword')}</p>
          <div className="flex gap-2">
            <Button type="submit" size="xs" disabled={busy}>
              {t('common.save')}
            </Button>
            <Button size="xs" variant="secondary" disabled={busy} onClick={() => setDraft(null)}>
              {t('common.cancel')}
            </Button>
          </div>
        </form>
      </li>
    )
  }

  return (
    <li className="flex flex-wrap items-center justify-between gap-2 rounded-xs border border-line bg-bright px-3 py-2">
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium text-ink">{user.name || user.email}</p>
        {user.name && <p className="truncate text-xs text-ink-soft">{user.email}</p>}
      </div>
      {user.admin ? (
        <span className="text-xs font-semibold uppercase tracking-[0.14em] text-amber-700">{t('managementUsers.admin')}</span>
      ) : (
        <div className="flex items-center gap-2">
          <Button
            size="xs"
            variant="secondary"
            disabled={busy}
            onClick={() => setDraft({ email: user.email, name: user.name, password: '' })}
          >
            {t('common.edit')}
          </Button>
          <Button size="xs" variant="danger" disabled={busy} onClick={() => void onDelete(user)}>
            {t('common.delete')}
          </Button>
        </div>
      )}
    </li>
  )
}

export function ManagementUsersPage() {
  const { data: users, loading, error: loadError, reload } = useAsync(listManagedUsers, [])
  const [draft, setDraft] = useState<Draft>(emptyDraft)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  async function run(action: () => Promise<string>): Promise<boolean> {
    try {
      setBusy(true)
      setError('')
      setNotice('')
      setNotice(await action())
      await reload()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : t('managementUsers.failed'))
      return false
    } finally {
      setBusy(false)
    }
  }

  async function onCreate(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const created = await run(async () => {
      const user = await createManagedUser({
        email: draft.email.trim(),
        name: draft.name.trim(),
        password: draft.password,
      })
      return t('managementUsers.added', { email: user.email })
    })
    if (created) setDraft(emptyDraft)
  }

  function onSave(user: ManagedUser, input: ManagedUserInput) {
    return run(async () => {
      await updateManagedUser(user.id, input)
      return t('managementUsers.saved', { email: input.email || user.email })
    })
  }

  async function onDelete(user: ManagedUser) {
    if (
      !window.confirm(
        t('managementUsers.deleteConfirm', { email: user.email }),
      )
    ) {
      return
    }
    await run(async () => {
      await deleteManagedUser(user.id)
      return t('managementUsers.deleted', { email: user.email })
    })
  }

  const rows = users ?? []

  return (
    <div className="flex flex-col gap-5">
      <section className={sectionClassName}>
        <h2 className={`${sectionTitleClassName} mb-3`}>{t('managementUsers.accounts')}</h2>

        {loadError && <p className="mb-3 text-sm text-madder">{loadError}</p>}
        {error && <p className="mb-3 text-sm text-madder">{error}</p>}
        {notice && <p className="mb-3 text-sm text-ink-soft">{notice}</p>}

        {loading ? (
          <p className="text-sm text-ink-soft">{t('managementUsers.loading')}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {rows.map((user) => (
              <UserRow key={user.id} user={user} busy={busy} onSave={onSave} onDelete={onDelete} />
            ))}
          </ul>
        )}
        <p className={`${fieldHintClassName} mt-3`}>
          {t('managementUsers.adminHint')}
        </p>
      </section>

      <section className={sectionClassName}>
        <h2 className={`${sectionTitleClassName} mb-3`}>{t('managementUsers.addTitle')}</h2>
        <form className="flex flex-col gap-3" onSubmit={onCreate}>
          <UserFields draft={draft} onChange={setDraft} passwordLabel={t('managementUsers.password')} />
          <p className={fieldHintClassName}>
            {t('managementUsers.addHint')}
          </p>
          <div>
            <Button type="submit" disabled={busy || !draft.email.trim() || !draft.password}>
              {t('managementUsers.add')}
            </Button>
          </div>
        </form>
      </section>
    </div>
  )
}
