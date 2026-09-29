/** What the header says about an instance that stops taking changes at `until` (ms). */
export function writableCountdown(until: number, now: number): { text: string; note: string } {
  const left = Math.ceil((until - now) / 1000)
  if (left <= 0) {
    return { text: 'Read-only', note: 'deleted soon' }
  }
  const h = Math.floor(left / 3600)
  const m = Math.floor((left % 3600) / 60)
  const s = String(left % 60).padStart(2, '0')
  const clock = h > 0 ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`
  return { text: `${clock} left`, note: 'then read-only, deleted soon after' }
}
