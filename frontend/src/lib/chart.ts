/**
 * A chart a research answer embeds, as the server writes it into a ```chart
 * block. backend/internal/report draws the same charts into PDF reports with
 * the same palette, ticks and number format.
 */
export type ChartSpec = {
  type: 'bar' | 'line' | 'pie'
  title: string
  labels: string[]
  series: { name?: string; values: number[] }[]
}

/** The categorical order, fixed: a series keeps its colour by position, never by rank. */
export const CHART_COLORS = [
  '#2a78d6',
  '#eb6834',
  '#1baf7a',
  '#eda100',
  '#e87ba4',
  '#008300',
  '#6250d6',
  '#e34948',
]

/** The largest value a chart takes, as report.MaxValue on the server. */
const MAX_VALUE = 1e15

/** The chart in a ```chart block, or null when it is not one this page can draw. */
export function parseChart(source: string): ChartSpec | null {
  let spec: ChartSpec
  try {
    spec = JSON.parse(source) as ChartSpec
  } catch {
    return null
  }
  const drawable =
    ['bar', 'line', 'pie'].includes(spec?.type) &&
    Array.isArray(spec.labels) &&
    Array.isArray(spec.series) &&
    spec.series.length > 0 &&
    spec.series.length <= CHART_COLORS.length &&
    spec.series.every(
      (s) =>
        Array.isArray(s.values) &&
        s.values.length === spec.labels.length &&
        s.values.every((v) => typeof v === 'number' && Math.abs(v) <= MAX_VALUE),
    )
  return drawable && (spec.type !== 'pie' || drawablePie(spec.series)) ? spec : null
}

function drawablePie(series: ChartSpec['series']) {
  return series.length === 1 && series[0].values.every((v) => v >= 0) && series[0].values.some((v) => v > 0)
}

/**
 * One bar's slot in a band shared by n series, and the gap that separates it
 * from its neighbour. The gap shrinks with the bar, so a crowded chart still
 * has a bar to draw.
 */
export function barWidth(band: number, n: number): [number, number] {
  const w = Math.min((band * 0.72) / n, 24)
  return [w, Math.min(2, w / 3)]
}

/** The value range the axis shows; bars grow from zero, so it always includes zero for them. */
export function extent(spec: ChartSpec): [number, number] {
  const values = spec.series.flatMap((s) => s.values)
  const lo = Math.min(...values)
  const hi = Math.max(...values)
  return spec.type === 'bar' ? [Math.min(lo, 0), Math.max(hi, 0)] : [lo, hi]
}

/**
 * About five round steps spanning lo..hi. A range float steps cannot divide --
 * too wide, too narrow, or too far from zero for its width -- gets its two ends
 * instead.
 */
export function niceTicks(lo: number, hi: number): number[] {
  if (hi <= lo) hi = lo + Math.max(1, Math.abs(lo))
  const raw = (hi - lo) / 5
  const unit = 10 ** Math.floor(Math.log10(raw))
  const step = ([1, 2, 5].find((f) => raw <= f * unit) ?? 10) * unit
  const first = Math.floor(lo / step)
  const n = Math.ceil(hi / step) - first
  if (!(n >= 1 && n <= 20)) return [lo, hi]
  const ticks = Array.from({ length: n + 1 }, (_, k) => (first + k) * step)
  return ticks[n] > ticks[0] ? ticks : [lo, hi]
}

/** Three significant digits, trailing zeros dropped. */
export function compact(v: number): string {
  if (v === 0) return '0'
  const digits = Math.min(12, Math.max(0, 2 - Math.floor(Math.log10(Math.abs(v)))))
  const s = v.toFixed(digits)
  return s.includes('.') ? s.replace(/\.?0+$/, '') : s
}

/** A value as an axis or a label shows it: 950, 1.2K, 3.4M. */
export function formatNumber(v: number): string {
  for (const [div, suffix] of [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ] as const) {
    if (Math.abs(v) >= div) return compact(v / div) + suffix
  }
  return compact(v)
}
