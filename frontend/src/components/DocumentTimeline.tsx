import type { DocumentTimeline as DocumentTimelineData } from '../lib/api/documents'
import { groupByYear, monthLabel } from '../lib/timeline'

type DocumentTimelineProps = {
  timeline: DocumentTimelineData | null
  /** The selected period ("2025" or "2025-03"), derived from the date filters. */
  active: string | null
  /** Called with the clicked period, or null when the active one is clicked again. */
  onSelect: (period: string | null) => void
  /** Shown in full, or collapsed to the hairline that expands it again. */
  expanded: boolean
  onToggleExpanded: () => void
  className?: string
}

function ChevronIcon({ expanded }: { expanded: boolean }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 20 20"
      fill="currentColor"
      className={`h-4 w-4 transition-transform ${expanded ? '' : '-rotate-90'}`}
      aria-hidden="true"
    >
      <path
        fillRule="evenodd"
        d="M5.23 7.21a.75.75 0 0 1 1.06.02L10 10.94l3.71-3.71a.75.75 0 1 1 1.06 1.06l-4.24 4.24a.75.75 0 0 1-1.06 0L5.21 8.29a.75.75 0 0 1 .02-1.08Z"
        clipRule="evenodd"
      />
    </svg>
  )
}

const rowClassName =
  'flex w-full items-baseline justify-between gap-2 border-l-2 py-1 pr-2 text-left transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood'

function rowStateClassName(isActive: boolean) {
  return isActive
    ? 'border-oxblood bg-wash text-oxblood'
    : 'border-transparent text-ink-muted hover:border-line hover:bg-surface'
}

/**
 * The archive's shape in time: every month that holds documents, grouped under
 * its year, with counts.
 *
 * Selecting a row writes the page's From/To date filters rather than keeping a
 * date filter of its own, so there is only one date filter to reason about and
 * the date inputs show what was picked. `active` is derived back out of those
 * inputs, which is why a hand-typed whole month lights its row up too.
 */
export function DocumentTimeline({
  timeline,
  active,
  onSelect,
  expanded,
  onToggleExpanded,
  className = '',
}: DocumentTimelineProps) {
  if (!timeline) return null

  const years = groupByYear(timeline.months)
  if (years.length === 0 && timeline.undated === 0) return null

  function select(period: string) {
    onSelect(active === period ? null : period)
  }

  // Collapsed, the sidebar is nothing but the rule that used to edge it, and
  // that rule is the way back: a full-height hairline whose hit area is wide
  // enough to actually hit. No landmark here on purpose — there is no content
  // to land on, only the control that reveals it.
  if (!expanded) {
    return (
      <button
        type="button"
        aria-label="Show timeline"
        title="Show timeline"
        aria-expanded={false}
        onClick={onToggleExpanded}
        className={`group flex items-center justify-center focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood ${className}`}
      >
        <span className="h-px w-full bg-line transition-colors group-hover:bg-oxblood lg:h-full lg:w-px" />
      </button>
    )
  }

  return (
    <aside className={className} aria-labelledby="timeline-heading">
      {/* The whole heading is the collapse control, not just the chevron: a
          16px icon is a small target for something this coarse. */}
      <h3 id="timeline-heading" className="mb-2 border-b border-line pb-2">
        <button
          type="button"
          title="Hide timeline"
          aria-expanded
          aria-controls="timeline-periods"
          onClick={onToggleExpanded}
          className="flex w-full items-center justify-between gap-1 text-[11px] font-semibold uppercase tracking-[0.12em] text-ink-soft transition-colors hover:text-oxblood focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood"
        >
          <span>Timeline</span>
          <ChevronIcon expanded />
        </button>
      </h3>
      <div id="timeline-periods" className="flex flex-col gap-3 text-sm">
        {years.map((year) => (
          <div key={year.year}>
            <button
              type="button"
              aria-pressed={active === year.year}
              data-timeline-period={year.year}
              onClick={() => select(year.year)}
              className={`${rowClassName} pl-2 font-display font-semibold ${rowStateClassName(
                active === year.year,
              )}`}
            >
              <span>{year.year}</span>
              <span className="text-xs tabular-nums text-ink-faint">{year.count}</span>
            </button>
            <div className="flex flex-col">
              {year.months.map((month) => (
                <button
                  key={month.month}
                  type="button"
                  aria-pressed={active === month.month}
                  data-timeline-period={month.month}
                  onClick={() => select(month.month)}
                  className={`${rowClassName} pl-4 ${rowStateClassName(active === month.month)}`}
                >
                  <span>{monthLabel(month.month)}</span>
                  <span className="text-xs tabular-nums text-ink-faint">{month.count}</span>
                </button>
              ))}
            </div>
          </div>
        ))}

        {/* Not a button: a document with no date sits outside every date range,
            so there is nothing for a click to select. Shown anyway so the
            counts add up to the library. */}
        {timeline.undated > 0 && (
          <p className="flex items-baseline justify-between gap-2 border-t border-line pl-2 pr-2 pt-2 text-xs text-ink-faint">
            <span>No date</span>
            <span className="tabular-nums">{timeline.undated}</span>
          </p>
        )}
      </div>
    </aside>
  )
}
