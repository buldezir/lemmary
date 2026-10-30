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
        s.values.every((v) => typeof v === 'number' && Number.isFinite(v)),
    )
  return drawable ? spec : null
}

/** The value range the axis shows; bars grow from zero, so it always includes zero for them. */
export function extent(spec: ChartSpec): [number, number] {
  const values = spec.series.flatMap((s) => s.values)
  const lo = Math.min(...values)
  const hi = Math.max(...values)
  return spec.type === 'bar' ? [Math.min(lo, 0), Math.max(hi, 0)] : [lo, hi]
}

/** About five round steps spanning lo..hi. */
export function niceTicks(lo: number, hi: number): number[] {
  if (hi <= lo) hi = lo + 1
  const raw = (hi - lo) / 5
  const unit = 10 ** Math.floor(Math.log10(raw))
  const step = ([1, 2, 5].find((f) => raw <= f * unit) ?? 10) * unit
  const ticks: number[] = []
  for (let i = Math.floor(lo / step); i <= Math.ceil(hi / step); i++) {
    ticks.push(i * step)
  }
  return ticks
}

/** Three significant digits, trailing zeros dropped. */
export function compact(v: number): string {
  if (v === 0) return '0'
  const digits = Math.max(0, 2 - Math.floor(Math.log10(Math.abs(v))))
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
