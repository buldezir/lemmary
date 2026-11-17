import { t } from '../../i18n'
import { apiFetch } from '../apiClient'

export type FeedbackKind = 'bug' | 'idea' | 'other'

export const logMinuteChoices = [0, 1, 2, 3, 5] as const
export type LogMinutes = (typeof logMinuteChoices)[number]

export type Feedback = {
  kind: FeedbackKind
  message: string
  email: string
  page: string
  /** Admins only; 0 attaches none. */
  log_minutes: LogMinutes
}

export function sendFeedback(body: Feedback) {
  return apiFetch<void>('/api/app/feedback', {
    method: 'POST',
    body,
    fallbackError: t('feedback.sendFailed'),
  })
}
