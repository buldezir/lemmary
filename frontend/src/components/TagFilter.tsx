import type { TagRecord } from '../lib/api/tags'
import { Combobox } from './Combobox'

/**
 * Narrows the list to documents carrying every chosen tag. The chips sit under
 * the box rather than over it so the row's three controls stay on one line.
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

  return (
    <div className="flex w-full flex-col gap-1.5 sm:w-64">
      <Combobox
        value=""
        options={available.map((tag) => ({ value: tag.id, label: tag.name }))}
        placeholder={value.length > 0 ? 'Add another tag...' : 'All tags'}
        ariaLabel="Filter by tag"
        bgClassName="bg-surface"
        disabled={available.length === 0}
        onChange={(id) => onChange([...value, id])}
      />
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
    </div>
  )
}
