import type { TagRecord } from '../lib/api/tags'
import { Combobox } from './Combobox'

/**
 * Narrows the list to documents carrying every chosen tag. Its own row rather
 * than a cell in the filter grid: the chips wrap to a height the grid's other
 * controls would have to match.
 */
export function TagFilter({
  value,
  options,
  onChange,
}: {
  value: string[]
  options: TagRecord[]
  onChange: (next: string[]) => void
}) {
  const byId = new Map(options.map((tag) => [tag.id, tag]))
  const available = options.filter((tag) => !value.includes(tag.id))

  if (options.length === 0 && value.length === 0) return null

  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs font-medium text-ink-soft">Tags</span>
      {value.length > 0 && (
        <ul className="flex flex-wrap gap-1.5">
          {value.map((id) => {
            // An id with no name behind it is a tag deleted since the link was
            // made; it still has to be removable.
            const name = byId.get(id)?.name ?? 'Unknown tag'
            return (
              <li
                key={id}
                className="flex items-center gap-1 rounded-xs border border-line-strong bg-wash px-2 py-1 text-xs text-ink"
              >
                {name}
                <button
                  type="button"
                  aria-label={`Remove ${name}`}
                  className="text-ink-faint transition-colors hover:text-madder"
                  onClick={() => onChange(value.filter((other) => other !== id))}
                >
                  &times;
                </button>
              </li>
            )
          })}
        </ul>
      )}
      {available.length > 0 && (
        <Combobox
          value=""
          options={available.map((tag) => ({ value: tag.id, label: tag.name }))}
          placeholder={value.length > 0 ? 'Add another tag...' : 'All tags'}
          ariaLabel="Filter by tag"
          bgClassName="bg-surface"
          className="sm:max-w-xs"
          onChange={(id) => onChange([...value, id])}
        />
      )}
    </div>
  )
}
