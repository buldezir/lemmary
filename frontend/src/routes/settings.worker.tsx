import { type SubmitEvent } from 'react'

import { useSettingsForm } from '../hooks/useSettingsForm'
import {
  ResultDialog,
  SaveSettingsButton,
  SettingsLoading,
} from '../components/settings/SettingsFeedback'
import {
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'

export function SettingsWorkerPage() {
  const { form, loading, error, success, saving, updateField, save, setError, closeResult } =
    useSettingsForm((settings) => ({
      worker_timeout_sec: String(settings.worker_timeout_sec),
      worker_max_retries: String(settings.worker_max_retries),
    }))

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return

    const workerTimeout = Number(form.worker_timeout_sec)
    const maxRetries = Number(form.worker_max_retries)
    if (!Number.isFinite(workerTimeout) || workerTimeout <= 0) {
      setError('Worker timeout must be a positive number')
      return
    }
    if (!Number.isFinite(maxRetries) || maxRetries < 0) {
      setError('Worker max retries must be >= 0')
      return
    }

    await save({ worker_timeout_sec: workerTimeout, worker_max_retries: maxRetries })
  }

  if (loading || !form) return <SettingsLoading error={error} />

  return (
    <form onSubmit={onSubmit}>
      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>Worker</h2>
        <div className="grid gap-4 sm:grid-cols-2">
          <label className={labelClassName}>
            <span className={labelTextClassName}>Job timeout (seconds)</span>
            <input
              type="number"
              min={1}
              className={inputClassName}
              value={form.worker_timeout_sec}
              onChange={(e) => updateField('worker_timeout_sec', e.target.value)}
            />
          </label>
          <label className={labelClassName}>
            <span className={labelTextClassName}>Max retries</span>
            <input
              type="number"
              min={0}
              className={inputClassName}
              value={form.worker_max_retries}
              onChange={(e) => updateField('worker_max_retries', e.target.value)}
            />
          </label>
        </div>
        <p className="mt-3 text-xs text-ink-soft">
          Worker cron schedule stays in <code className="font-mono">WORKER_CRON_EXPR</code> in{' '}
          <code className="font-mono">.env</code>.
        </p>
        <div className="mt-4">
          <SaveSettingsButton saving={saving} />
        </div>
      </section>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </form>
  )
}
