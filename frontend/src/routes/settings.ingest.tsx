import { type ChangeEvent, type SubmitEvent } from 'react'

import { useAppMeta } from '../hooks/useAppMeta'
import { useSettingsForm } from '../hooks/useSettingsForm'
import { useAsync } from '../hooks/useAsync'
import { listUsers } from '../lib/api/users'
import type { AppSettings, AppSettingsPatch, ImapFileType } from '../lib/api/settings'
import {
  ResultDialog,
  SaveSettingsButton,
  SettingsLoading,
} from '../components/settings/SettingsFeedback'
import {
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
  selectClassName,
} from '../components/ui'
import { lang, t, tNode } from '../i18n'

// config.ValidIngestInterval: the steps a cron schedule spaces evenly.
const SCAN_INTERVALS = [1, 2, 5, 10, 15, 30, 60, 120, 180, 360, 720, 1440]

const subTitleClassName = 'mb-3 mt-6 text-sm font-semibold text-ink'

function intervalLabel(minutes: number) {
  if (minutes === 1440) return t('settingsIngest.everyDay')
  if (minutes === 60) return t('settingsIngest.everyHour')
  if (minutes > 60) return t('settingsIngest.everyHours', { count: minutes / 60 })
  if (minutes === 1) return t('settingsIngest.everyMinute')
  return t('settingsIngest.everyMinutes', { count: minutes })
}

const imapFileTypes: [ImapFileType, string][] = [
  ['pdf', 'PDF'],
  ['office', 'Office (DOCX, XLSX)'],
  ['image', t('settingsIngest.typeImages')],
  ['text', t('settingsIngest.typeText')],
]

/** The consume folder and the IMAP mailbox. Reachable when INGEST_DIR or INGEST_IMAP_ENABLED is set. */
export function SettingsIngestPage() {
  const { ingestDir, ingestImap } = useAppMeta()
  const { form, loading, error, success, saving, updateField, updateFields, save, closeResult } =
    useSettingsForm((settings) => ({
      ingest_dir_owner: settings.ingest_dir_owner,
      ingest_dir_interval_min: String(settings.ingest_dir_interval_min),
      ingest_dir_delete_original: settings.ingest_dir_delete_original,
      imap_host: settings.imap_host,
      imap_security: settings.imap_security,
      imap_username: settings.imap_username,
      imap_password: '',
      imap_password_set: settings.imap_password_set,
      imap_folder: settings.imap_folder,
      imap_after_consume: settings.imap_after_consume,
      imap_move_folder: settings.imap_move_folder,
      imap_skip_types: settings.imap_skip_types,
      imap_since: settings.imap_since,
    }))
  const { data: users } = useAsync(listUsers, [])

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return

    const patch: AppSettingsPatch = {
      ingest_dir_owner: form.ingest_dir_owner,
      ingest_dir_interval_min: Number(form.ingest_dir_interval_min),
    }
    if (ingestDir) patch.ingest_dir_delete_original = form.ingest_dir_delete_original
    if (ingestImap) {
      Object.assign(patch, {
        imap_host: form.imap_host,
        imap_security: form.imap_security,
        imap_username: form.imap_username,
        imap_password: form.imap_password,
        imap_folder: form.imap_folder,
        imap_after_consume: form.imap_after_consume,
        imap_move_folder: form.imap_move_folder,
        imap_skip_types: form.imap_skip_types,
      })
    }
    await save(patch)
  }

  function onOwnerChange(owner: string) {
    const email = users?.find((user) => user.id === owner)?.email ?? ''
    updateFields((current) => ({
      ingest_dir_owner: owner,
      ...(ingestImap &&
        /@(gmail|googlemail)\.com$/i.test(email) &&
        !current.imap_host &&
        !current.imap_username && {
          imap_host: 'imap.gmail.com',
          imap_username: email,
          imap_folder: current.imap_folder || 'INBOX',
        }),
    }))
  }

  if (loading || !form) return <SettingsLoading error={error} />

  const text = (
    key: 'imap_host' | 'imap_username' | 'imap_password' | 'imap_folder' | 'imap_move_folder',
  ) => ({
    className: inputClassName,
    value: form[key],
    onChange: (e: ChangeEvent<HTMLInputElement>) => updateField(key, e.target.value),
    autoComplete: 'off',
  })

  return (
    <form onSubmit={onSubmit}>
      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>{t('settingsIngest.title')}</h2>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>{t('settingsIngest.owner')}</span>
              <select
                className={selectClassName}
                value={form.ingest_dir_owner}
                onChange={(e) => onOwnerChange(e.target.value)}
              >
                <option value="">{t('settingsIngest.ownerDefault')}</option>
                {(users ?? []).map((user) => (
                  <option key={user.id} value={user.id}>
                    {user.name ? `${user.name} (${user.email})` : user.email}
                  </option>
                ))}
              </select>
            </label>
            <p className={fieldHintClassName}>{t('settingsIngest.ownerHint')}</p>
          </div>
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>{t('settingsIngest.scanEvery')}</span>
              <select
                className={selectClassName}
                value={form.ingest_dir_interval_min}
                onChange={(e) => updateField('ingest_dir_interval_min', e.target.value)}
              >
                {SCAN_INTERVALS.map((minutes) => (
                  <option key={minutes} value={String(minutes)}>
                    {intervalLabel(minutes)}
                  </option>
                ))}
              </select>
            </label>
            {ingestDir && (
              <p className={fieldHintClassName}>
                {t('settingsIngest.scanHint')}
              </p>
            )}
          </div>
        </div>

        {ingestDir && (
          <>
            <h3 className={subTitleClassName}>{t('settingsIngest.folder')}</h3>
            <p className={`${fieldHintClassName} mb-4`}>
              {tNode('settingsIngest.folderHint', { path: <code>Taxes/2024/invoice.pdf</code> })}
            </p>
            <div className="rounded-xs border border-line-strong bg-bright p-4">
              <label className="flex items-center gap-2.5 text-sm font-medium text-ink">
                <input
                  type="checkbox"
                  className="h-4 w-4 accent-oxblood"
                  checked={form.ingest_dir_delete_original}
                  onChange={(e) => updateField('ingest_dir_delete_original', e.target.checked)}
                />
                {t('settingsIngest.deleteOriginal')}
              </label>
              <p className={`${fieldHintClassName} mt-2`}>
                {t('settingsIngest.deleteOriginalHint')}
              </p>
            </div>
          </>
        )}

        {ingestImap && (
          <>
            <h3 className={subTitleClassName}>{t('settingsIngest.mailbox')}</h3>
            <p className={`${fieldHintClassName} mb-4`}>
              {t('settingsIngest.mailboxHint')}
              {form.imap_since &&
                ` ${t('settingsIngest.mailboxSince', { date: new Date(form.imap_since).toLocaleString(lang) })}`}
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('settingsIngest.imapServer')}</span>
                <input {...text('imap_host')} placeholder="imap.example.com" />
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('settingsIngest.security')}</span>
                <select
                  className={selectClassName}
                  value={form.imap_security}
                  onChange={(e) => updateField('imap_security', e.target.value as AppSettings['imap_security'])}
                >
                  <option value="tls">{t('settingsIngest.securityTls')}</option>
                  <option value="starttls">{t('settingsIngest.securityStarttls')}</option>
                </select>
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('settingsIngest.username')}</span>
                <input {...text('imap_username')} />
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('settingsIngest.password')}</span>
                <input
                  {...text('imap_password')}
                  type="password"
                  placeholder={form.imap_password_set ? t('settingsIngest.passwordUnchanged') : ''}
                />
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('settingsIngest.folder')}</span>
                <input {...text('imap_folder')} placeholder="INBOX" />
              </label>
              <label className={labelClassName}>
                <span className={labelTextClassName}>{t('settingsIngest.afterImport')}</span>
                <select
                  className={selectClassName}
                  value={form.imap_after_consume}
                  onChange={(e) =>
                    updateField('imap_after_consume', e.target.value as AppSettings['imap_after_consume'])
                  }
                >
                  <option value="keep">{t('settingsIngest.afterKeep')}</option>
                  <option value="move">{t('settingsIngest.afterMove')}</option>
                  <option value="delete">{t('settingsIngest.afterDelete')}</option>
                </select>
              </label>
              {form.imap_after_consume === 'move' && (
                <label className={labelClassName}>
                  <span className={labelTextClassName}>{t('settingsIngest.moveFolder')}</span>
                  <input {...text('imap_move_folder')} placeholder="Lemmary/Done" required />
                </label>
              )}
            </div>
            <fieldset className="mt-4">
              <legend className={labelTextClassName}>{t('settingsIngest.importTypes')}</legend>
              <div className="mt-2 flex flex-wrap gap-x-6 gap-y-2">
                {imapFileTypes.map(([type, label]) => (
                  <label key={type} className="flex items-center gap-2.5 text-sm font-medium text-ink">
                    <input
                      type="checkbox"
                      className="h-4 w-4 accent-oxblood"
                      checked={!form.imap_skip_types.includes(type)}
                      disabled={
                        !form.imap_skip_types.includes(type) &&
                        form.imap_skip_types.length === imapFileTypes.length - 1
                      }
                      onChange={(e) =>
                        updateField(
                          'imap_skip_types',
                          e.target.checked
                            ? form.imap_skip_types.filter((skip) => skip !== type)
                            : [...form.imap_skip_types, type],
                        )
                      }
                    />
                    {label}
                  </label>
                ))}
              </div>
            </fieldset>
            <p className={`${fieldHintClassName} mt-2`}>
              {t('settingsIngest.keptHint')}
            </p>
          </>
        )}

        <div className="mt-4">
          <SaveSettingsButton saving={saving} />
        </div>
      </section>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </form>
  )
}
