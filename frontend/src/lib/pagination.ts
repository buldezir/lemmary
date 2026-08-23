/**
 * Which page numbers to render: everything up to 7 pages, otherwise the first,
 * the last, and a window around the current page (gaps become ellipses).
 */
export function pageNumbers(current: number, total: number): number[] {
  if (total <= 7) {
    return Array.from({ length: total }, (_, index) => index + 1)
  }

  const pages = new Set<number>([1, total, current, current - 1, current + 1])
  return [...pages].filter((page) => page >= 1 && page <= total).sort((a, b) => a - b)
}
