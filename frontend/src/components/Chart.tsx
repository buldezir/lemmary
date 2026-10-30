import { t } from '../i18n'
import { CHART_COLORS, compact, extent, formatNumber, niceTicks, type ChartSpec } from '../lib/chart'

const W = 640
const H = 240
const PAD = { top: 10, right: 8, bottom: 26, left: 44 }
const CHAR_W = 6.2
const VALUE_LABEL_LIMIT = 16
const MARKER_LIMIT = 24

/** A chart from a research answer: SVG marks, a legend, and the figures as a table. */
export function Chart({ spec }: { spec: ChartSpec }) {
  const names = spec.series.map((s, i) => s.name || `${i + 1}`)
  return (
    <figure className="mb-3 last:mb-0">
      <figcaption className="mb-1 text-sm font-semibold">{spec.title}</figcaption>
      {spec.type === 'pie' ? <Pie spec={spec} /> : <Axes spec={spec} />}
      {(spec.type === 'pie' || spec.series.length > 1) && (
        <ul className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-muted">
          {(spec.type === 'pie' ? spec.labels : names).map((name, i) => (
            <li key={i} className="flex items-center gap-1.5">
              <span className="inline-block size-2.5" style={{ backgroundColor: CHART_COLORS[i] }} />
              {name}
              {spec.type === 'pie' && <PieShare values={spec.series[0].values} index={i} />}
            </li>
          ))}
        </ul>
      )}
      <details className="mt-1 text-xs text-ink-soft">
        <summary className="cursor-pointer">{t('chart.showData')}</summary>
        <DataTable spec={spec} names={names} />
      </details>
    </figure>
  )
}

function PieShare({ values, index }: { values: number[]; index: number }) {
  const total = values.reduce((a, b) => a + b, 0)
  return (
    <span className="text-ink-faint">
      {formatNumber(values[index])} · {compact((values[index] / total) * 100)}%
    </span>
  )
}

function DataTable({ spec, names }: { spec: ChartSpec; names: string[] }) {
  return (
    <div className="mt-1 overflow-x-auto">
      <table className="min-w-full border-collapse text-left">
        <thead className="border-b border-line-strong">
          <tr>
            <th className="px-2 py-1 font-semibold" />
            {names.map((name, i) => (
              <th key={i} className="px-2 py-1 text-right font-semibold">
                {spec.series.length > 1 ? name : t('chart.value')}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {spec.labels.map((label, row) => (
            <tr key={row}>
              <td className="border-t border-line px-2 py-1">{label}</td>
              {spec.series.map((s, col) => (
                <td key={col} className="border-t border-line px-2 py-1 text-right tabular-nums">
                  {s.values[row].toLocaleString()}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function fit(label: string, width: number) {
  const chars = Math.max(1, Math.floor(width / CHAR_W))
  return label.length > chars ? `${label.slice(0, Math.max(1, chars - 1))}…` : label
}

/** A bar's outline: rounded at the data end, square at the baseline. */
function barPath(x: number, w: number, base: number, end: number) {
  const h = Math.abs(base - end)
  const r = Math.min(4, w / 2, h)
  const dir = end < base ? 1 : -1
  return `M${x},${base}V${end + dir * r}Q${x},${end} ${x + r},${end}H${x + w - r}Q${x + w},${end} ${x + w},${end + dir * r}V${base}Z`
}

function Axes({ spec }: { spec: ChartSpec }) {
  const ticks = niceTicks(...extent(spec))
  const lo = ticks[0]
  const hi = ticks[ticks.length - 1]
  const plotW = W - PAD.left - PAD.right
  const plotH = H - PAD.top - PAD.bottom
  const y = (v: number) => PAD.top + plotH - ((v - lo) / (hi - lo)) * plotH
  const band = plotW / spec.labels.length
  const x = (i: number) => PAD.left + (i + 0.5) * band
  const widest = Math.max(...spec.labels.map((l) => l.length)) * CHAR_W
  const every = Math.max(1, Math.ceil((Math.min(widest, 120) + 8) / band))

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label={spec.title}>
      {ticks.map((tick) => (
        <g key={tick}>
          <line x1={PAD.left} x2={W - PAD.right} y1={y(tick)} y2={y(tick)} stroke="var(--color-wash)" />
          <text x={PAD.left - 6} y={y(tick) + 4} textAnchor="end" fontSize="11" fill="var(--color-ink-soft)">
            {formatNumber(tick)}
          </text>
        </g>
      ))}
      {spec.labels.map((label, i) =>
        i % every === 0 ? (
          <text key={i} x={x(i)} y={H - 8} textAnchor="middle" fontSize="11" fill="var(--color-ink-soft)">
            {fit(label, band * every - 4)}
          </text>
        ) : null,
      )}
      {spec.type === 'bar' ? <Bars spec={spec} band={band} y={y} /> : <Lines spec={spec} x={x} y={y} />}
    </svg>
  )
}

function Bars({ spec, band, y }: { spec: ChartSpec; band: number; y: (v: number) => number }) {
  const n = spec.series.length
  const w = Math.min((band * 0.72) / n, 24)
  const start = (band - w * n) / 2
  const labelled = n === 1 && spec.labels.length <= VALUE_LABEL_LIMIT
  return spec.series.flatMap((series, s) =>
    series.values.map((v, i) => {
      const bx = PAD.left + i * band + start + s * w
      return (
        <g key={`${s}-${i}`}>
          {v !== 0 && <path d={barPath(bx + 1, w - 2, y(0), y(v))} fill={CHART_COLORS[s]} />}
          {labelled && (
            <text
              x={bx + w / 2}
              y={v < 0 ? y(v) + 12 : y(v) - 4}
              textAnchor="middle"
              fontSize="10"
              fill="var(--color-ink)"
            >
              {formatNumber(v)}
            </text>
          )}
          <rect x={bx} y={PAD.top} width={w} height={H - PAD.top - PAD.bottom} fill="transparent">
            <title>{`${series.name ? `${series.name} · ` : ''}${spec.labels[i]}: ${v.toLocaleString()}`}</title>
          </rect>
        </g>
      )
    }),
  )
}

function Lines({ spec, x, y }: { spec: ChartSpec; x: (i: number) => number; y: (v: number) => number }) {
  const markers = spec.labels.length <= MARKER_LIMIT
  return spec.series.map((series, s) => (
    <g key={s}>
      <polyline
        points={series.values.map((v, i) => `${x(i)},${y(v)}`).join(' ')}
        fill="none"
        stroke={CHART_COLORS[s]}
        strokeWidth="2"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
      {series.values.map((v, i) => (
        <circle
          key={i}
          cx={x(i)}
          cy={y(v)}
          r={markers ? 4 : 8}
          fill={markers ? CHART_COLORS[s] : 'transparent'}
          stroke={markers ? 'var(--color-paper)' : 'none'}
          strokeWidth="2"
        >
          <title>{`${series.name ? `${series.name} · ` : ''}${spec.labels[i]}: ${v.toLocaleString()}`}</title>
        </circle>
      ))}
    </g>
  ))
}

function Pie({ spec }: { spec: ChartSpec }) {
  const values = spec.series[0].values
  const total = values.reduce((a, b) => a + b, 0)
  const r = 100
  const point = (turn: number) => {
    const a = turn * 2 * Math.PI - Math.PI / 2
    return `${110 + r * Math.cos(a)},${110 + r * Math.sin(a)}`
  }
  const starts = values.map((_, i) => values.slice(0, i).reduce((a, b) => a + b, 0) / total)
  return (
    <svg viewBox="0 0 220 220" className="w-full max-w-56" role="img" aria-label={spec.title}>
      {values.map((v, i) => {
        const share = v / total
        const from = starts[i]
        const at = from + share
        const title = <title>{`${spec.labels[i]}: ${v.toLocaleString()} (${compact(share * 100)}%)`}</title>
        if (share >= 1) {
          return (
            <circle key={i} cx="110" cy="110" r={r} fill={CHART_COLORS[i]}>
              {title}
            </circle>
          )
        }
        if (share <= 0) return null
        return (
          <path
            key={i}
            d={`M110,110L${point(from)}A${r},${r} 0 ${share > 0.5 ? 1 : 0} 1 ${point(at)}Z`}
            fill={CHART_COLORS[i]}
            stroke="var(--color-paper)"
            strokeWidth="2"
            strokeLinejoin="round"
          >
            {title}
          </path>
        )
      })}
    </svg>
  )
}
