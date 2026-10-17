import { type SubmitEvent, useState } from 'react'
import { changePassword, getMe } from '../lib/auth'
import {
  deletePasskey,
  listPasskeys,
  passkeyDateLabel,
  registerPasskey,
  renamePasskey,
  type Passkey,
} from '../lib/api/passkeys'
import { defaultPasskeyName, passkeysSupported, passkeyUnavailableHint } from '../lib/webauthn'
import { createMCPToken, getMCPStatus } from '../lib/api/mcp'
import { createPaperlessToken } from '../lib/api/users'
import { pbUrl } from '../lib/pb'
import { mcpSnippets } from '../lib/mcpSnippets'
import { useAsync } from '../hooks/useAsync'
import { getLimits } from '../lib/api/limits'
import { LimitsUsage } from '../components/LimitsUsage'
import { SectionLayout } from '../components/SectionLayout'
import { savePreferences, useCardLayout, type CardLayout, type Preferences } from '../lib/userSettings'
import {
  Button,
  DocsLink,
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
  selectClassName,
} from '../components/ui'
import { docsUrl, lang, languageNames, setLanguage, t, tNode, type Language, type MessageKey } from '../i18n'

function SignedInSection() {
  const { data: me } = useAsync(getMe, [])

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>{t('account.signedInAs')}</h2>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm text-ink">{me?.email || '...'}</span>
        {me?.is_admin && (
          <span className="border border-line-strong px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.12em] text-ink-soft">
            {t('account.admin')}
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
            aria-label={t('account.passkeyName')}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            className={`${inputClassName} max-w-xs flex-1`}
          />
          <Button type="submit" size="xs" disabled={busy}>
            {t('common.save')}
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
            {t('common.cancel')}
          </Button>
        </form>
      ) : (
        <>
          <div className="min-w-0">
            <p className="truncate text-sm font-medium text-ink">{passkey.name}</p>
            <p className="text-xs text-ink-soft">
              {t('account.passkeyDates', {
                created: passkeyDateLabel(passkey.created),
                used: passkeyDateLabel(passkey.last_used),
              })}
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
              {t('account.rename')}
            </Button>
            <Button
              size="xs"
              variant="danger"
              disabled={busy}
              onClick={() => void onDelete(passkey)}
            >
              {t('common.delete')}
            </Button>
          </div>
        </>
      )}
    </li>
  )
}

export function AccountPasskeysPage() {
  const { data: passkeys, error: loadError, reload } = useAsync(listPasskeys, [])
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  // Only enrolling needs a working ceremony: listing and deleting must keep
  // working on an address that cannot run one, or a passkey enrolled over HTTPS
  // could never be cleaned up from a plain-HTTP LAN address.
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
      setNotice(t('account.passkeyAdded', { name: created.name }))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('account.addFailed'))
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
      setError(err instanceof Error ? err.message : t('account.renameFailed'))
    } finally {
      setBusy(false)
    }
  }

  async function onDelete(passkey: Passkey) {
    const lastOne = rows.length === 1
    const warning = lastOne
      ? t('account.removeLastConfirm', { name: passkey.name })
      : t('account.removeConfirm', { name: passkey.name })
    if (!window.confirm(warning)) {
      return
    }
    try {
      setBusy(true)
      setError('')
      setNotice('')
      await deletePasskey(passkey.id)
      await reload()
      setNotice(t('account.passkeyRemoved', { name: passkey.name }))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('account.removeFailed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>{t('account.passkeys')}</h2>
      <p className={`${fieldHintClassName} mb-4`}>
        {t('account.passkeysHint')}
      </p>

      {loadError && <p className="mb-3 text-sm text-madder">{loadError}</p>}
      {error && <p className="mb-3 text-sm text-madder">{error}</p>}
      {notice && <p className="mb-3 text-sm text-ink-soft">{notice}</p>}

      {!passkeys ? null : rows.length === 0 ? (
        <p className="mb-4 text-sm text-ink-soft">{t('account.noPasskeys')}</p>
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
          {t('account.addPasskey')}
        </Button>
      )}

      {canEnroll && adding && (
        <form className="flex flex-col gap-3" onSubmit={onAdd}>
          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('account.name')}</span>
            <input
              value={name}
              onChange={(event) => setName(event.target.value)}
              className={`${inputClassName} max-w-sm`}
            />
          </label>
          <p className={fieldHintClassName}>{t('account.nameHint')}</p>
          <div className="flex items-center gap-2">
            <Button type="submit" disabled={busy}>
              {busy ? t('account.waiting') : t('account.createPasskey')}
            </Button>
            <Button variant="secondary" disabled={busy} onClick={() => setAdding(false)}>
              {t('common.cancel')}
            </Button>
          </div>
        </form>
      )}
    </section>
  )
}

const tokenPlaceholder = '<token>'

export function AccountAgentsPage() {
  const { data: status, error: loadError } = useAsync(getMCPStatus, [])
  const [token, setToken] = useState('')
  const [agent, setAgent] = useState('claude-code')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)

  const snippets = status?.enabled ? mcpSnippets(status.url, token || tokenPlaceholder) : []
  const current = snippets.find((snippet) => snippet.id === agent) ?? snippets[0]

  async function onCreateToken() {
    setBusy(true)
    setError('')
    setCopied(false)
    try {
      setToken(await createMCPToken())
    } catch (err) {
      setError(err instanceof Error ? err.message : t('account.tokenFailed'))
    } finally {
      setBusy(false)
    }
  }

  async function onCopy() {
    if (!current) {
      return
    }
    try {
      await navigator.clipboard.writeText(current.text)
      setCopied(true)
    } catch {
      setError(t('account.copyFailed'))
    }
  }

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>{t('account.agents')}</h2>
      <p className={`${fieldHintClassName} mb-4`}>
        {tNode('account.agentsHint', {
          link: <DocsLink href={docsUrl('mcp.html')}>{t('account.mcpLink')}</DocsLink>,
        })}
      </p>

      {loadError && <p className="mb-3 text-sm text-madder">{loadError}</p>}
      {status && !status.enabled && (
        <p className="text-sm text-ink-soft">
          {tNode('account.mcpOff', { env: <code>MCP_ENABLED=0</code> })}
        </p>
      )}

      {status?.enabled && current && (
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <Button onClick={onCreateToken} disabled={busy}>
              {token ? t('account.createAnotherToken') : t('account.createToken')}
            </Button>
            <span className={fieldHintClassName}>
              {token
                ? t('account.tokenShown')
                : t('account.tokenHint')}
            </span>
          </div>
          {error && <p className="text-sm text-madder">{error}</p>}

          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('account.agent')}</span>
            <select
              value={current.id}
              onChange={(event) => {
                setAgent(event.target.value)
                setCopied(false)
              }}
              className={`${selectClassName} max-w-xs`}
            >
              {snippets.map((snippet) => (
                <option key={snippet.id} value={snippet.id}>
                  {snippet.name}
                </option>
              ))}
            </select>
          </label>

          <div>
            <div className="mb-1 flex flex-wrap items-center justify-between gap-2">
              <span className={fieldHintClassName}>{current.where}</span>
              <Button size="xs" variant="secondary" onClick={onCopy}>
                {copied ? t('account.copied') : t('account.copy')}
              </Button>
            </div>
            <pre
              data-testid="mcp-snippet"
              className="overflow-x-auto rounded-xs border border-line bg-bright p-3 text-xs text-ink"
            >
              {current.text}
            </pre>
            {!token && (
              <p className={`${fieldHintClassName} mt-1`}>
                {tNode('account.replaceToken', { token: <code>{tokenPlaceholder}</code> })}
              </p>
            )}
          </div>
        </div>
      )}
    </section>
  )
}

export function AccountPaperlessPage() {
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)

  async function onCreateToken() {
    setBusy(true)
    setError('')
    setCopied(false)
    try {
      setToken(await createPaperlessToken())
    } catch (err) {
      setError(err instanceof Error ? err.message : t('account.tokenFailed'))
    } finally {
      setBusy(false)
    }
  }

  async function onCopy() {
    try {
      await navigator.clipboard.writeText(token)
      setCopied(true)
    } catch {
      setError(t('account.copyFailed'))
    }
  }

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>{t('account.paperlessApps')}</h2>
      <p className={`${fieldHintClassName} mb-4`}>
        {tNode('account.paperlessAppsHint', {
          url: <code>{pbUrl}</code>,
          link: <DocsLink href={docsUrl('paperless_ngx.html')}>{t('account.paperlessLink')}</DocsLink>,
        })}
      </p>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={onCreateToken} disabled={busy}>
            {token ? t('account.createAnotherToken') : t('account.createToken')}
          </Button>
          <span className={fieldHintClassName}>
            {token ? t('account.tokenShown') : t('account.paperlessTokenHint')}
          </span>
        </div>
        {error && <p className="text-sm text-madder">{error}</p>}
        {token && (
          <div>
            <div className="mb-1 flex justify-end">
              <Button size="xs" variant="secondary" onClick={onCopy}>
                {copied ? t('account.copied') : t('account.copy')}
              </Button>
            </div>
            <pre
              data-testid="paperless-token"
              className="whitespace-pre-wrap break-all rounded-xs border border-line bg-bright p-3 text-xs text-ink"
            >
              {token}
            </pre>
          </div>
        )}
      </div>
    </section>
  )
}

export function AccountPasswordPage() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeated, setRepeated] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    setNotice('')
    if (next !== repeated) {
      setError(t('account.passwordMismatch'))
      return
    }
    try {
      setBusy(true)
      await changePassword(current, next)
      setCurrent('')
      setNext('')
      setRepeated('')
      setNotice(t('account.passwordChanged'))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('account.passwordFailed'))
    } finally {
      setBusy(false)
    }
  }

  const fields = [
    { label: t('account.currentPassword'), value: current, set: setCurrent, autoComplete: 'current-password' },
    { label: t('account.newPassword'), value: next, set: setNext, autoComplete: 'new-password' },
    { label: t('account.confirmPassword'), value: repeated, set: setRepeated, autoComplete: 'new-password' },
  ]

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>{t('account.password')}</h2>
      <p className={`${fieldHintClassName} mb-4`}>{t('account.passwordHint')}</p>
      <form className="flex flex-col gap-3" onSubmit={onSubmit}>
        {fields.map((field) => (
          <label key={field.label} className={labelClassName}>
            <span className={labelTextClassName}>{field.label}</span>
            <input
              type="password"
              required
              minLength={field.autoComplete === 'new-password' ? 8 : undefined}
              autoComplete={field.autoComplete}
              value={field.value}
              onChange={(event) => field.set(event.target.value)}
              className={`${inputClassName} max-w-sm`}
            />
          </label>
        ))}
        {error && <p className="text-sm text-madder">{error}</p>}
        {notice && <p className="text-sm text-ink-soft">{notice}</p>}
        <div>
          <Button type="submit" disabled={busy}>
            {t('account.changePassword')}
          </Button>
        </div>
      </form>
    </section>
  )
}

function InstanceLimitsSection() {
  const { data: limits } = useAsync(getLimits, [])
  if (!limits || !(limits.enforced || (limits.misconfigured?.length ?? 0) > 0)) return null

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>{t('account.limitsTitle')}</h2>
      <p className="text-xs text-ink-soft">{t('account.limitsHint')}</p>
      {limits.enforced ? (
        <LimitsUsage limits={limits} className="mt-4 border-0 bg-transparent p-0" />
      ) : (
        <p className="mt-4 text-xs text-ink-soft">{t('account.noLimits')}</p>
      )}
      {(limits.misconfigured?.length ?? 0) > 0 && (
        <p className="mt-4 text-sm text-madder">
          {t('account.limitsMisconfigured', { names: limits.misconfigured?.join(', ') ?? '' })}
        </p>
      )}
      {limits.enforced && (
        <p className="mt-4 text-xs text-ink-faint">{t('account.limitsUpgrade')}</p>
      )}
    </section>
  )
}

export function AccountPreferencesPage() {
  const layout = useCardLayout()
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  /** True once saved; the caller then applies it. */
  async function save(patch: Preferences) {
    setSaving(true)
    setError('')
    try {
      await savePreferences(patch)
      return true
    } catch {
      setError(t('account.preferencesFailed'))
      return false
    } finally {
      setSaving(false)
    }
  }

  // Not switched when the save fails: the account's own language would win
  // again on the next load, and the switch would not stick.
  async function onLanguageChange(next: Language) {
    if (await save({ language: next })) {
      setLanguage(next)
    }
  }

  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>{t('account.preferences')}</h2>
      <label className={`${labelClassName} mb-6`}>
        <span className={labelTextClassName}>{t('common.language')}</span>
        <select
          className={`${selectClassName} max-w-xs`}
          value={lang}
          disabled={saving}
          onChange={(event) => void onLanguageChange(event.target.value as Language)}
        >
          {Object.entries(languageNames).map(([code, name]) => (
            <option key={code} value={code}>
              {name}
            </option>
          ))}
        </select>
        <span className={fieldHintClassName}>{t('account.languageHint')}</span>
      </label>
      <fieldset disabled={saving}>
        <legend className={labelTextClassName}>{t('account.cardLayout')}</legend>
        <div className="mt-2 grid max-w-xl grid-cols-2 gap-4">
          {CARD_LAYOUT_OPTIONS.map(({ layout: option, label }) => (
            <label key={option} className="group cursor-pointer">
              <input
                type="radio"
                name="card-layout"
                value={option}
                checked={layout === option}
                onChange={() => void save({ card_layout: option })}
                className="peer sr-only"
              />
              <div className="border-2 border-transparent p-1.5 transition-colors group-hover:border-line peer-checked:border-oxblood! peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-oxblood">
                <CardLayoutExample layout={option} />
              </div>
              <span className="mt-2 block text-sm font-medium text-ink-muted peer-checked:text-oxblood">
                {t(label)}
              </span>
            </label>
          ))}
        </div>
        <p className={`${fieldHintClassName} mt-3`}>{t('account.cardLayoutHint')}</p>
      </fieldset>
      {error && <p className="mt-3 text-sm text-madder">{error}</p>}
    </section>
  )
}

const CARD_LAYOUT_OPTIONS = [
  { layout: 'summary', label: 'account.cardLayoutSummary' },
  { layout: 'preview', label: 'account.cardLayoutPreview' },
] as const satisfies readonly { layout: CardLayout; label: MessageKey }[]

/** A document card drawn in outline: the same frame either way, and only what sits under the title differs. */
function CardLayoutExample({ layout }: { layout: CardLayout }) {
  const tags = (
    <div className={`flex gap-1.5 ${layout === 'preview' ? 'absolute inset-x-2 bottom-2 *:bg-surface' : ''}`}>
      <span className="h-3 w-10 border border-line" />
      <span className="h-3 w-8 border border-line" />
    </div>
  )

  return (
    <div aria-hidden="true" className="flex flex-col gap-2.5 border border-line bg-surface p-3">
      <div className="flex items-center justify-between">
        <span className="h-2.5 w-14 border border-line-strong" />
        <span className="h-2 w-10 bg-line" />
      </div>
      <div className="border-t border-line pt-2.5">
        <div className="h-3 w-4/5 bg-ink/70" />
        <div className="mt-1.5 h-2 w-1/2 bg-line-strong" />
      </div>
      {layout === 'summary' ? (
        <div className="flex h-20 flex-col gap-1.5">
          <div className="h-2 w-full bg-line" />
          <div className="h-2 w-full bg-line" />
          <div className="h-2 w-3/5 bg-line" />
        </div>
      ) : (
        <div className="relative flex aspect-[1/1.13] w-full flex-col gap-1.5 overflow-hidden border border-line bg-bright px-3 pt-3">
          <div className="h-1.5 w-1/3 bg-ink-faint" />
          <div className="h-1.5 w-1/2 bg-line-strong" />
          <div className="mt-1.5 h-1 w-full bg-line" />
          <div className="h-1 w-full bg-line" />
          <div className="h-1 w-4/5 bg-line" />
          <div className="h-1 w-full bg-line" />
          {tags}
        </div>
      )}
      {layout === 'summary' && tags}
    </div>
  )
}

export function AccountProfilePage() {
  return (
    <div className="flex flex-col gap-5">
      <SignedInSection />
      <InstanceLimitsSection />
    </div>
  )
}

export function AccountPage() {
  return (
    <SectionLayout
      title={t('account.title')}
      intro={t('account.intro')}
      label={t('account.sectionsLabel')}
      links={[
        { to: '/account', label: t('account.profile'), exact: true },
        { to: '/account/preferences', label: t('account.preferences') },
        { to: '/account/agents', label: t('account.agents') },
        { to: '/account/paperless', label: t('account.paperlessApps') },
        { to: '/account/password', label: t('account.password') },
        { to: '/account/passkeys', label: t('account.passkeys') },
      ]}
    />
  )
}
