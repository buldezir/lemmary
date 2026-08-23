import { pageNumbers } from '../lib/pagination'

type Props = {
  page: number
  totalPages: number
  totalItems: number
  pageSize: number
  onPageChange: (page: number) => void
}

const buttonClassName =
  'rounded-xs border border-line-strong bg-surface px-3 py-1.5 text-sm font-medium text-ink-muted transition-colors hover:bg-bright disabled:cursor-not-allowed disabled:opacity-40'

export function Pagination({ page, totalPages, totalItems, pageSize, onPageChange }: Props) {
  if (totalPages <= 1) {
    return null
  }

  const start = (page - 1) * pageSize + 1
  const end = Math.min(page * pageSize, totalItems)
  const pages = pageNumbers(page, totalPages)

  return (
    <div className="flex flex-col items-center justify-between gap-3 sm:flex-row">
      <p className="font-mono text-xs tabular-nums text-ink-soft">
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
              {showEllipsis && <span className="px-1 text-sm text-ink-faint">…</span>}
              <button
                type="button"
                onClick={() => onPageChange(pageNumber)}
                aria-current={pageNumber === page ? 'page' : undefined}
                className={`min-w-9 rounded-xs px-2 py-1.5 text-sm font-medium transition-colors ${
                  pageNumber === page
                    ? 'bg-ink text-paper hover:bg-oxblood'
                    : 'text-ink-muted hover:text-oxblood'
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
