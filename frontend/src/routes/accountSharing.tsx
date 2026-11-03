import { useState } from 'react'
import { Navigate } from '@tanstack/react-router'
import { listShareRecipients } from '../lib/api/shares'
import { listLibraryShares, revokeLibraryShare, shareLibrary } from '../lib/api/libraryShares'
import { useAsync } from '../hooks/useAsync'
import { t } from '../i18n'
import { Combobox } from '../components/Combobox'
import { Button, sectionClassName, sectionTitleClassName } from '../components/ui'

export function AccountSharingPage() {
  const { data, error, reload } = useAsync(async () => {
    const [users, grants] = await Promise.all([listShareRecipients(), listLibraryShares()])
    return { users, grants }
  }, [])
  const [busy, setBusy] = useState(false)
  const [changeError, setChangeError] = useState('')

  async function run(action: () => Promise<void>) {
    setBusy(true)
    setChangeError('')
    try {
      await action()
      await reload()
    } catch (err) {
      setChangeError(err instanceof Error ? err.message : t('shareDialog.changeFailed'))
    } finally {
      setBusy(false)
    }
  }

  if (data && data.users.length === 0) return <Navigate to="/account" replace />
  if (!data) {
    return (
      <p
        role={error ? 'alert' : undefined}
        className={error ? 'text-sm text-madder' : 'text-sm text-ink-soft'}
      >
        {error || t('shareDialog.loading')}
      </p>
    )
  }

  const granted = new Set(data.grants.map((grant) => grant.recipient))
  const available = data.users.filter((user) => !granted.has(user.id))
  return (
    <section className={sectionClassName}>
      <h2 className={sectionTitleClassName}>{t('account.sharing')}</h2>
      <p className="text-sm text-ink-soft">{t('librarySharing.description')}</p>
      {(error || changeError) && (
        <p role="alert" className="text-sm text-madder">{error || changeError}</p>
      )}
      <Combobox
        value=""
        options={available.map((user) => ({
          value: user.id,
          label: user.name ? `${user.name} (${user.email})` : user.email,
        }))}
        placeholder={t('shareDialog.addAccount')}
        ariaLabel={t('shareDialog.shareWith')}
        disabled={busy || available.length === 0}
        onChange={(id) => void run(() => shareLibrary(id))}
      />
      {data.grants.length === 0 ? (
        <p className="text-sm text-ink-soft">{t('librarySharing.empty')}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {data.grants.map((grant) => {
            const user = data.users.find((candidate) => candidate.id === grant.recipient)
            return (
              <li key={grant.id} className="flex items-center justify-between gap-2 border border-line px-3 py-2 text-sm">
                <span>
                  {user?.name ? `${user.name} (${user.email})` : user?.email ?? t('shareDialog.unknownAccount')}
                </span>
                <Button
                  size="xs"
                  variant="secondary"
                  disabled={busy}
                  onClick={() => void run(() => revokeLibraryShare(grant.id))}
                >
                  {t('common.remove')}
                </Button>
              </li>
            )
          })}
        </ul>
      )}
      <p className="text-sm text-ink-soft">{t('librarySharing.revokeHint')}</p>
    </section>
  )
}
