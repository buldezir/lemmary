type Props = {
  page: number
  totalPages: number
  totalItems: number
  pageSize: number
  onPageChange: (page: number) => void
}

const buttonClassName =
  'rounded-md border border-stone-300 bg-stone-50 px-3 py-1.5 text-sm font-medium text-stone-700 transition-colors hover:bg-white disabled:cursor-not-allowed disabled:opacity-40'

function pageNumbers(current: number, total: number): number[] {
  if (total <= 7) {
    return Array.from({ length: total }, (_, index) => index + 1)
  }

  const pages = new Set<number>([1, total, current, current - 1, current + 1])
  return [...pages].filter((page) => page >= 1 && page <= total).sort((a, b) => a - b)
}

export function Pagination({ page, totalPages, totalItems, pageSize, onPageChange }: Props) {
  if (totalPages <= 1) {
    return null
  }

  const start = (page - 1) * pageSize + 1
  const end = Math.min(page * pageSize, totalItems)
  const pages = pageNumbers(page, totalPages)

  return (
    <div className="flex flex-col items-center justify-between gap-3 sm:flex-row">
      <p className="text-sm text-stone-500">
        Showing {start}–{end} of {totalItems}
      </p>

      <nav aria-label="Pagination" className="flex items-center gap-1">
        <button
          type="button"
          onClick={() => onPageChange(page - 1)}
          disabled={page <= 1}
          className={buttonClassName}
        >
          Previous
        </button>

        {pages.map((pageNumber, index) => {
          const previous = pages[index - 1]
          const showEllipsis = previous !== undefined && pageNumber - previous > 1

          return (
            <span key={pageNumber} className="flex items-center gap-1">
              {showEllipsis && <span className="px-1 text-sm text-stone-400">…</span>}
              <button
                type="button"
                onClick={() => onPageChange(pageNumber)}
                aria-current={pageNumber === page ? 'page' : undefined}
                className={`min-w-9 rounded-md px-2 py-1.5 text-sm font-medium transition-colors hover:bg-stone-600 hover:text-stone-100 ${
                  pageNumber === page
                    ? 'bg-gray-900 text-white'
                    : 'text-stone-600 hover:bg-stone-100'
                }`}
              >
                {pageNumber}
              </button>
            </span>
          )
        })}

        <button
          type="button"
          onClick={() => onPageChange(page + 1)}
          disabled={page >= totalPages}
          className={buttonClassName}
        >
          Next
        </button>
      </nav>
    </div>
  )
}
