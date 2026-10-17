import { useId } from 'react'
import { t } from '../i18n'
import { PAGE_SIZES, pageNumbers } from '../lib/pagination'
import { Combobox } from './Combobox'

type Props = {
  page: number
  totalPages: number
  totalItems: number
  pageSize: number
  onPageChange: (page: number) => void
  onPageSizeChange: (size: number) => void
}

const pageSizeOptions = PAGE_SIZES.map((size) => ({ value: String(size), label: String(size) }))

const buttonClassName =
  'rounded-xs border border-line-strong bg-surface px-3 py-1.5 text-sm font-medium text-ink-muted transition-colors hover:bg-bright disabled:cursor-not-allowed disabled:opacity-40'

export function Pagination({
  page,
  totalPages,
  totalItems,
  pageSize,
  onPageChange,
  onPageSizeChange,
}: Props) {
  const pageSizeId = useId()

  // Kept while the list fits on one page at a larger size, so it can be shrunk again.
  if (totalItems <= PAGE_SIZES[0]) {
    return null
  }

  const start = (page - 1) * pageSize + 1
  const end = Math.min(page * pageSize, totalItems)
  const pages = pageNumbers(page, totalPages)

  return (
    <div className="flex flex-col items-center justify-between gap-3 sm:flex-row">
      <div className="flex items-center gap-3">
        <p className="font-mono text-xs tabular-nums text-ink-soft">
          {t('pagination.showing', { start, end, total: totalItems })}
        </p>
        <div className="flex items-center gap-2 text-xs text-ink-soft">
          <label htmlFor={pageSizeId}>{t('pagination.perPage')}</label>
          <Combobox
            id={pageSizeId}
            value={String(pageSize)}
            options={pageSizeOptions}
            onChange={(next) => onPageSizeChange(Number(next))}
            bgClassName="bg-surface"
            className="w-24"
          />
        </div>
      </div>

      {totalPages > 1 && (
        <nav aria-label={t('pagination.label')} className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => onPageChange(page - 1)}
            disabled={page <= 1}
            className={buttonClassName}
          >
            {t('pagination.previous')}
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
            {t('pagination.next')}
          </button>
        </nav>
      )}
    </div>
  )
}
