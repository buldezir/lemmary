import type { ProcessingSummary } from '../lib/processing'

const toneStyles: Record<ProcessingSummary['tone'], { marker: string; text: string }> = {
  running: { marker: 'animate-pulse text-oxblood', text: 'text-ink-muted' },
  warning: { marker: 'text-amber-800', text: 'text-amber-800' },
  error: { marker: 'text-madder', text: 'text-madder' },
}

const markers: Record<ProcessingSummary['tone'], string> = {
  running: '·',
  warning: '⚠',
  error: '✗',
}

/**
 * One line saying what is happening to a document, or what went wrong.
 *
 * Sits beside the status badge rather than replacing it: the badge is the
 * document's own processing_status, this is the job's account of it, and they
 * disagree on purpose -- a "completed" document whose embeddings failed softly
 * is exactly the case this exists to show.
 */
export function ProcessingStatus({ summary }: { summary: ProcessingSummary | null }) {
  if (!summary) return null
  const tone = toneStyles[summary.tone]

  return (
    // Clamped and titled: a provider error is persisted up to 1900 characters,
    // and a card is not the place to print all of it.
    <p className={`flex items-baseline gap-1.5 text-xs ${tone.text}`} title={summary.detail}>
      <span aria-hidden className={`font-mono ${tone.marker}`}>
        {markers[summary.tone]}
      </span>
      <span className="line-clamp-2 min-w-0">
        <span className="font-medium">{summary.label}</span>
        {summary.detail ? <span className="text-ink-soft"> — {summary.detail}</span> : null}
      </span>
    </p>
  )
}
