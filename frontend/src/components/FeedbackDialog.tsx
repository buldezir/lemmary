import { useEffect, useRef, useState, type FormEvent } from 'react'
import { t } from '../i18n'
import { logMinuteChoices, sendFeedback, type FeedbackKind, type LogMinutes } from '../lib/api/feedback'
import { pb } from '../lib/pb'
import { Combobox } from './Combobox'
import { Button, fieldHintClassName, inputClassName, labelClassName, labelTextClassName } from './ui'

const kinds: FeedbackKind[] = ['bug', 'idea', 'other']

/** Sends a report to the Lemmary maintainers, relayed by the server. Mounted only while open. */
export function FeedbackDialog({ admin, onClose }: { admin: boolean; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null)
  const [kind, setKind] = useState<FeedbackKind>('bug')
  const [message, setMessage] = useState('')
  const [email, setEmail] = useState(() => (pb.authStore.record?.email as string | undefined) ?? '')
  const [logMinutes, setLogMinutes] = useState<LogMinutes>(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [sent, setSent] = useState(false)

  useEffect(() => {
    ref.current?.showModal()
  }, [])
  const close = () => ref.current?.close()

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await sendFeedback({
        kind,
        message,
        email: email.trim(),
        page: window.location.pathname,
        log_minutes: admin ? logMinutes : 0,
      })
      setSent(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('feedback.sendFailed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      aria-labelledby="feedback-title"
      className="m-auto w-full max-w-lg border border-line bg-surface p-5 text-ink backdrop:bg-ink/40"
    >
      <h2 id="feedback-title" className="font-display text-lg font-semibold text-ink">
        {t('feedback.title')}
      </h2>
      {sent ? (
        <>
          <p className="mt-3 text-sm text-ink-muted" role="status">
            {t('feedback.thanks')}
          </p>
          <div className="mt-5 flex justify-end">
            <Button onClick={close}>{t('common.close')}</Button>
          </div>
        </>
      ) : (
        <form onSubmit={(event) => void submit(event)} className="mt-1 flex flex-col gap-4">
          <p className="text-sm text-ink-soft">{t('feedback.description')}</p>
          <fieldset className="flex flex-wrap gap-4">
            <legend className={`${labelTextClassName} mb-1`}>{t('feedback.kind')}</legend>
            {kinds.map((value) => (
              <label key={value} className="flex items-center gap-2 text-sm text-ink-muted">
                <input
                  type="radio"
                  name="feedback-kind"
                  value={value}
                  checked={kind === value}
                  onChange={() => setKind(value)}
                  className="size-4 border-line-strong text-oxblood focus:ring-oxblood"
                />
                {t(`feedback.kind.${value}` as const)}
              </label>
            ))}
          </fieldset>
          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('feedback.message')}</span>
            <textarea
              required
              maxLength={5000}
              rows={6}
              value={message}
              onChange={(event) => setMessage(event.target.value)}
              className={inputClassName}
            />
          </label>
          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('feedback.email')}</span>
            <input
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              className={inputClassName}
            />
            <span className={fieldHintClassName}>{t('feedback.emailHint')}</span>
          </label>
          {admin && (
            <div className={labelClassName}>
              <span className={labelTextClassName}>{t('feedback.logs')}</span>
              <Combobox
                ariaLabel={t('feedback.logs')}
                value={String(logMinutes)}
                options={logMinuteChoices.map((minutes) => ({
                  value: String(minutes),
                  label: minutes ? t('feedback.logs.minutes', { count: minutes }) : t('feedback.logs.none'),
                }))}
                onChange={(next) => setLogMinutes(Number(next) as LogMinutes)}
                bgClassName="bg-surface"
              />
              <span className={fieldHintClassName}>{t('feedback.logsHint')}</span>
            </div>
          )}
          {error && <p className="text-sm text-madder">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={close}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={busy || !message.trim()}>
              {busy ? t('feedback.sending') : t('feedback.send')}
            </Button>
          </div>
        </form>
      )}
    </dialog>
  )
}
