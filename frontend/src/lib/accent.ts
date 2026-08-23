/** Pick black or white text for contrast on an accent background. */
export function accentContrastText(accent: string): string {
  const hex = accent.trim().replace(/^#/, '')
  if (!/^[0-9a-fA-F]{6}$/.test(hex)) {
    return '#ffffff'
  }
  const r = Number.parseInt(hex.slice(0, 2), 16)
  const g = Number.parseInt(hex.slice(2, 4), 16)
  const b = Number.parseInt(hex.slice(4, 6), 16)
  const luma = (0.299 * r + 0.587 * g + 0.114 * b) / 255
  return luma > 0.6 ? '#000000' : '#ffffff'
}
