import { type FormEvent, useCallback, useEffect, useState } from 'react'
import {
  connectGmailAccount,
  deleteMailAccount,
  ensureAuth,
  getMailAccounts,
  getMailScans,
  getMailStatus,
  listAuthOAuth2Providers,
  patchMailAccount,
  pb,
  startMailScan,
  type MailAccount,
  type MailScan,
} from '../lib/pocketbase'

const inputClassName =
  'w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm outline-none focus:border-gray-900 focus:ring-1 focus:ring-gray-900'
const labelClassName = 'flex flex-col gap-1'
const labelTextClassName = 'text-xs font-medium text-stone-500'
const sectionClassName = 'rounded-lg border border-stone-200 bg-stone-50 p-5'
const sectionTitleClassName = 'mb-4 text-sm font-semibold text-stone-950'
const buttonClassName =
  'rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 disabled:opacity-50'
const secondaryButtonClassName =
  'rounded-md border border-stone-300 bg-white px-3 py-1.5 text-sm text-stone-700 hover:bg-stone-100 disabled:opacity-50'

function todayISO() {
  return new Date().toISOString().slice(0, 10)
}

function daysAgoISO(days: number) {
  const d = new Date()
  d.setDate(d.getDate() - days)
  return d.toISOString().slice(0, 10)
}

export function MailPage() {
  const [accounts, setAccounts] = useState<MailAccount[]>([])
  const [scans, setScans] = useState<MailScan[]>([])
  const [googleConfigured, setGoogleConfigured] = useState(false)
  const [googleProviderAvailable, setGoogleProviderAvailable] = useState(false)
  const [loading, setLoading] = useState(true)
  const [connecting, setConnecting] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [dateFrom, setDateFrom] = useState(daysAgoISO(30))
  const [dateTo, setDateTo] = useState(todayISO())
  const [scanMode, setScanMode] = useState<'simple' | 'deep'>('simple')
  const [scanningAccountId, setScanningAccountId] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    await ensureAuth()
    const [status, accountList, scanList, providers] = await Promise.all([
      getMailStatus(),
      getMailAccounts(),
      getMailScans(),
      listAuthOAuth2Providers(),
    ])
    setGoogleConfigured(status.google_oauth_configured)
    setGoogleProviderAvailable(providers.some((p) => p.name === 'google'))
    setAccounts(accountList)
    setScans(scanList)
  }, [])

  useEffect(() => {
    let active = true
    ;(async () => {
      try {
        await refresh()
        if (active) setError('')
      } catch (err) {
        if (active) setError(err instanceof Error ? err.message : 'Failed to load mail settings')
      } finally {
        if (active) setLoading(false)
      }
    })()
    return () => {
      active = false
    }
  }, [refresh])

  useEffect(() => {
    const hasActive = scans.some((s) => s.status === 'pending' || s.status === 'running')
    if (!hasActive) return
    const id = window.setInterval(() => {
      void getMailScans()
        .then(setScans)
        .catch(() => {})
    }, 2000)
    return () => window.clearInterval(id)
  }, [scans])

  async function onConnect() {
    setConnecting(true)
    setError('')
    setSuccess('')
    try {
      const previousToken = pb.authStore.token
      const previousRecord = pb.authStore.record
      const authData = await pb.collection('users').authWithOAuth2({
        provider: 'google',
        scopes: [
          'https://www.googleapis.com/auth/userinfo.profile',
          'https://www.googleapis.com/auth/userinfo.email',
          'https://www.googleapis.com/auth/gmail.readonly',
        ],
        urlCallback: (url) => {
          const next = new URL(url)
          next.searchParams.set('access_type', 'offline')
          next.searchParams.set('prompt', 'consent')
          window.open(next.toString(), 'gmail-oauth', 'width=600,height=700')
        },
      })

      const refreshToken = authData.meta?.refreshToken as string | undefined
      const email =
        (authData.meta?.email as string | undefined) ||
        authData.record?.email ||
        previousRecord?.email ||
        ''

      // Restore prior session if OAuth switched auth identity unexpectedly.
      if (previousToken && previousRecord && authData.record?.id !== previousRecord.id) {
        pb.authStore.save(previousToken, previousRecord)
      }

      if (!refreshToken) {
        throw new Error(
          'Google did not return a refresh token. Revoke app access in Google Account settings and try again with consent.',
        )
      }
      await connectGmailAccount(refreshToken, String(email))
      setSuccess('Gmail connected.')
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to connect Gmail')
    } finally {
      setConnecting(false)
    }
  }

  async function onPatchAccount(account: MailAccount, patch: Parameters<typeof patchMailAccount>[1]) {
    setError('')
    try {
      await patchMailAccount(account.id, patch)
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update account')
    }
  }

  async function onDisconnect(account: MailAccount) {
    setError('')
    try {
      await deleteMailAccount(account.id)
      setSuccess('Gmail disconnected.')
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to disconnect')
    }
  }

  async function onStartScan(event: FormEvent, account: MailAccount) {
    event.preventDefault()
    setError('')
    setSuccess('')
    setScanningAccountId(account.id)
    try {
      await startMailScan(account.id, { date_from: dateFrom, date_to: dateTo, mode: scanMode })
      setSuccess('Scan started.')
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to start scan')
    } finally {
      setScanningAccountId(null)
    }
  }

  if (loading) {
    return (
      <main className="mx-auto max-w-3xl px-6 py-10">
        <p className="text-sm text-stone-500">Loading…</p>
      </main>
    )
  }

  const canConnect = googleConfigured && googleProviderAvailable

  return (
    <main className="mx-auto max-w-3xl space-y-6 px-6 py-10">
      <div>
        <h1 className="text-2xl font-semibold text-stone-950">Mail import</h1>
        <p className="mt-1 text-sm text-stone-600">
          Connect Gmail and import invoice attachments by date range or on a schedule.
        </p>
      </div>

      {error && (
        <p className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800" role="alert">
          {error}
        </p>
      )}
      {success && (
        <p className="rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
          {success}
        </p>
      )}

      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>Gmail account</h2>
        {!canConnect && (
          <p className="mb-4 text-sm text-stone-600">
            Enable the Google OAuth2 provider on the <code className="text-xs">users</code> collection in PocketBase
            Admin (and turn on the Gmail API in Google Cloud) before connecting.
          </p>
        )}
        {accounts.length === 0 ? (
          <button type="button" className={buttonClassName} disabled={!canConnect || connecting} onClick={onConnect}>
            {connecting ? 'Connecting…' : 'Connect Gmail'}
          </button>
        ) : (
          <ul className="space-y-4">
            {accounts.map((account) => (
              <li key={account.id} className="space-y-3 border-t border-stone-200 pt-4 first:border-0 first:pt-0">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <p className="text-sm font-medium text-stone-950">{account.email}</p>
                    <p className="text-xs text-stone-500">
                      {account.enabled ? 'Enabled' : 'Disabled'}
                      {account.last_synced_at ? ` · last sync ${new Date(account.last_synced_at).toLocaleString()}` : ''}
                    </p>
                  </div>
                  <div className="flex gap-2">
                    <button
                      type="button"
                      className={secondaryButtonClassName}
                      onClick={() => onPatchAccount(account, { enabled: !account.enabled })}
                    >
                      {account.enabled ? 'Disable' : 'Enable'}
                    </button>
                    <button type="button" className={secondaryButtonClassName} onClick={() => onDisconnect(account)}>
                      Disconnect
                    </button>
                  </div>
                </div>

                <div className="grid gap-3 sm:grid-cols-2">
                  <label className={labelClassName}>
                    <span className={labelTextClassName}>Default triage mode</span>
                    <select
                      className={inputClassName}
                      value={account.triage_mode}
                      onChange={(e) =>
                        onPatchAccount(account, { triage_mode: e.target.value as 'simple' | 'deep' })
                      }
                    >
                      <option value="simple">Simple (subject + filenames)</option>
                      <option value="deep">Deep (+ email body)</option>
                    </select>
                  </label>
                  <label className={labelClassName}>
                    <span className={labelTextClassName}>Cron lookback (days)</span>
                    <input
                      type="number"
                      min={1}
                      max={90}
                      className={inputClassName}
                      value={account.cron_lookback_days}
                      onChange={(e) =>
                        onPatchAccount(account, { cron_lookback_days: Number(e.target.value) || 7 })
                      }
                    />
                  </label>
                </div>

                <label className="flex items-center gap-2 text-sm text-stone-700">
                  <input
                    type="checkbox"
                    checked={account.cron_enabled}
                    onChange={(e) => onPatchAccount(account, { cron_enabled: e.target.checked })}
                  />
                  Automatic sync (cron)
                </label>

                <form className="space-y-3 border-t border-stone-200 pt-3" onSubmit={(e) => onStartScan(e, account)}>
                  <h3 className="text-xs font-semibold uppercase tracking-wide text-stone-500">Manual scan</h3>
                  <div className="grid gap-3 sm:grid-cols-3">
                    <label className={labelClassName}>
                      <span className={labelTextClassName}>From date</span>
                      <input
                        type="date"
                        required
                        className={inputClassName}
                        value={dateFrom}
                        onChange={(e) => setDateFrom(e.target.value)}
                      />
                    </label>
                    <label className={labelClassName}>
                      <span className={labelTextClassName}>To date</span>
                      <input
                        type="date"
                        required
                        className={inputClassName}
                        value={dateTo}
                        onChange={(e) => setDateTo(e.target.value)}
                      />
                    </label>
                    <label className={labelClassName}>
                      <span className={labelTextClassName}>Mode</span>
                      <select
                        className={inputClassName}
                        value={scanMode}
                        onChange={(e) => setScanMode(e.target.value as 'simple' | 'deep')}
                      >
                        <option value="simple">Simple</option>
                        <option value="deep">Deep</option>
                      </select>
                    </label>
                  </div>
                  <button
                    type="submit"
                    className={buttonClassName}
                    disabled={!account.enabled || scanningAccountId === account.id}
                  >
                    {scanningAccountId === account.id ? 'Starting…' : 'Start scan'}
                  </button>
                </form>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>Scan history</h2>
        {scans.length === 0 ? (
          <p className="text-sm text-stone-500">No scans yet.</p>
        ) : (
          <ul className="divide-y divide-stone-200">
            {scans.map((scan) => (
              <li key={scan.id} className="py-3 text-sm">
                <div className="flex flex-wrap items-baseline justify-between gap-2">
                  <p className="font-medium text-stone-950">
                    {scan.date_from} → {scan.date_to}{' '}
                    <span className="font-normal text-stone-500">({scan.mode}, {scan.trigger})</span>
                  </p>
                  <span className="text-xs uppercase tracking-wide text-stone-500">{scan.status}</span>
                </div>
                <p className="mt-1 text-xs text-stone-500">
                  listed {scan.progress.listed} · fetched {scan.progress.fetched} · imported {scan.progress.imported} ·
                  skipped {scan.progress.skipped} · dupes {scan.progress.duplicates} · errors {scan.progress.errors}
                </p>
                {scan.error && <p className="mt-1 text-xs text-red-700">{scan.error}</p>}
              </li>
            ))}
          </ul>
        )}
      </section>
    </main>
  )
}
