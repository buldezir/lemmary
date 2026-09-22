import { type SubmitEvent } from 'react'

import { useSettingsForm } from '../hooks/useSettingsForm'
import { useAsync } from '../hooks/useAsync'
import { listUsers } from '../lib/api/users'
import {
  ResultDialog,
  SaveSettingsButton,
  SettingsLoading,
} from '../components/settings/SettingsFeedback'
import {
  fieldHintClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
  selectClassName,
} from '../components/ui'

// config.ValidIngestInterval: the steps a cron schedule spaces evenly.
const SCAN_INTERVALS = [1, 2, 5, 10, 15, 30, 60, 120, 180, 360, 720, 1440]

function intervalLabel(minutes: number) {
  if (minutes === 1440) return 'day'
  if (minutes >= 60) return minutes === 60 ? 'hour' : `${minutes / 60} hours`
  return minutes === 1 ? 'minute' : `${minutes} minutes`
}

/** The consume folder. Only reachable when INGEST_DIR is set; see the settings tabs. */
export function SettingsIngestPage() {
  const { form, loading, error, success, saving, updateField, save, closeResult } =
    useSettingsForm((settings) => ({
      ingest_dir_owner: settings.ingest_dir_owner,
      ingest_dir_interval_min: String(settings.ingest_dir_interval_min),
      ingest_dir_delete_original: settings.ingest_dir_delete_original,
    }))
  const { data: users } = useAsync(listUsers, [])

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return

    await save({
      ingest_dir_owner: form.ingest_dir_owner,
      ingest_dir_interval_min: Number(form.ingest_dir_interval_min),
      ingest_dir_delete_original: form.ingest_dir_delete_original,
    })
  }

  if (loading || !form) return <SettingsLoading error={error} />

  return (
    <form onSubmit={onSubmit}>
      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>Ingest folder</h2>
        <p className={`${fieldHintClassName} mb-4`}>
          Files dropped into the mounted folder become documents on the next scan. Subfolders
          become tags, so <code>Taxes/2024/invoice.pdf</code> arrives tagged Taxes and 2024.
        </p>
        <div className="mb-4 rounded-xs border border-line-strong bg-bright p-4">
          <label className="flex items-center gap-2.5 text-sm font-medium text-ink">
            <input
              type="checkbox"
              className="h-4 w-4 accent-oxblood"
              checked={form.ingest_dir_delete_original}
              onChange={(e) => updateField('ingest_dir_delete_original', e.target.checked)}
            />
            Delete the original file after it is consumed
          </label>
          <p className={`${fieldHintClassName} mt-2`}>
            Off, files stay where they are and each is imported once, even if its document is
            deleted later; changing the file imports it again. On, the file is removed once its
            document exists, and a duplicate is removed too.
          </p>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Owner</span>
              <select
                className={selectClassName}
                value={form.ingest_dir_owner}
                onChange={(e) => updateField('ingest_dir_owner', e.target.value)}
              >
                <option value="">Default (first admin account)</option>
                {(users ?? []).map((user) => (
                  <option key={user.id} value={user.id}>
                    {user.name ? `${user.name} (${user.email})` : user.email}
                  </option>
                ))}
              </select>
            </label>
            <p className={fieldHintClassName}>The account that owns every document the folder yields.</p>
          </div>
          <div className={labelClassName}>
            <label className={labelClassName}>
              <span className={labelTextClassName}>Scan every</span>
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
            <p className={fieldHintClassName}>
              Files changed in the last 30 seconds wait for the next scan, so nothing is picked up
              half-written.
            </p>
          </div>
        </div>
        <div className="mt-4">
          <SaveSettingsButton saving={saving} />
        </div>
      </section>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </form>
  )
}
