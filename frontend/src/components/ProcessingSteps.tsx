import {
  formatDuration,
  stepDurationMs,
  stepLabel,
  type ProcessingJobRecord,
  type StepRunRecord,
} from '../lib/processing'

const markers: Record<StepRunRecord['status'], string> = {
  pending: '·',
  running: '·',
  completed: '✓',
  skipped: '–',
  failed: '✗',
}

function markerClass(run: StepRunRecord): string {
  if (run.status === 'running') return 'animate-pulse text-oxblood'
  if (run.status === 'failed') return run.soft ? 'text-amber-800' : 'text-madder'
  if (run.status === 'completed') return 'text-ink-faint'
  return 'text-ink-faint'
}

/**
 * `now` is threaded in rather than read here so a running step's elapsed time
 * ticks with the caller's clock.
 */
export function ProcessingSteps({
  job,
  now,
  collapsed = false,
}: {
  job: ProcessingJobRecord
  now: number
  collapsed?: boolean
}) {
  const runs = job.step_runs ?? []
  if (runs.length === 0) {
    return (
      <p className="text-xs text-ink-faint">
        {job.error ? job.error : 'No steps have run yet.'}
      </p>
    )
  }

  const list = (
    <ol className="flex flex-wrap items-baseline gap-x-1.5 text-xs text-ink-muted">
      {runs.map((run, index) => {
        const ms = stepDurationMs(run, now)
        return (
          <li key={run.name} className="flex items-baseline gap-x-1.5">
            {index > 0 ? (
              <span aria-hidden className="text-ink-faint">
                →
              </span>
            ) : null}
            <span aria-hidden className={`font-mono ${markerClass(run)}`}>
              {markers[run.status] ?? '·'}
            </span>
            <span
              className={run.status === 'pending' ? 'text-ink-faint' : 'text-ink'}
              // Attribution belongs on the step but not in the line: which
              // provider and model ran it only matters once you are asking why.
              title={[run.provider, run.model, run.prompt_version].filter(Boolean).join(' · ') || undefined}
            >
              {stepLabel(run.name)}
            </span>
            {ms !== null ? (
              <span className="tabular-nums text-ink-soft">
                {formatDuration(ms)}
                {run.status === 'running' ? '…' : ''}
              </span>
            ) : null}
            {run.status === 'skipped' ? <span className="text-ink-faint">skipped</span> : null}
            {run.attempts > 1 ? (
              <span className="text-ink-soft">attempt {run.attempts}</span>
            ) : null}
          </li>
        )
      })}
    </ol>
  )

  // Out of the row: a provider message is a paragraph, and keeping it inline
  // would break the chain of steps back into a list.
  const stepErrors = runs
    .filter((run) => run.error)
    .map((run) => (
      <p key={run.name} className={`mt-1 text-xs ${run.soft ? 'text-amber-800' : 'text-madder'}`}>
        {stepLabel(run.name)}: {run.error}
      </p>
    ))

  // Only when no step is carrying it: a failure outside any step leaves
  // step_runs untouched and this is the only place its message can appear,
  // but an older row can carry the same message twice.
  const unattributedError = runs.some((run) => run.status === 'failed' && run.error)
    ? ''
    : job.error

  const body = (
    <>
      {list}
      {stepErrors}
      {unattributedError ? (
        <p className="mt-1 text-xs text-madder">{unattributedError}</p>
      ) : null}
    </>
  )

  if (!collapsed) return <div className="border-l-2 border-line pl-3">{body}</div>
  return (
    <details className="border-l-2 border-line pl-3">
      <summary className="cursor-pointer text-xs text-ink-faint">
        {runs.length} step{runs.length === 1 ? '' : 's'}
      </summary>
      <div className="mt-1">{body}</div>
    </details>
  )
}
