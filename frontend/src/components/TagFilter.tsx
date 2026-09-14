import type { TagRecord } from '../lib/api/tags'
import { Combobox } from './Combobox'

/** Narrows the list to documents carrying every chosen tag. */
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

  const pills = value.map((id) => {
    // An id with no name behind it is a tag deleted since the link was made;
    // it still has to be removable.
    const name = byId.get(id)?.name ?? 'Unknown tag'
    return (
      <span
        key={id}
        className="flex max-w-full items-center gap-1 rounded-xs border border-line-strong bg-wash px-2 py-0.5 text-xs text-ink"
      >
        <span className="truncate">{name}</span>
        <button
          type="button"
          aria-label={`Remove ${name}`}
          className="shrink-0 text-ink-faint transition-colors hover:text-madder"
          onClick={() => onChange(value.filter((other) => other !== id))}
        >
          &times;
        </button>
      </span>
    )
  })

  return (
    <Combobox
      value=""
      options={available.map((tag) => ({ value: tag.id, label: tag.name }))}
      placeholder={value.length > 0 ? 'Add a tag...' : 'All tags'}
      ariaLabel="Filter by tag"
      bgClassName="bg-surface"
      className="w-full min-w-0 sm:flex-1"
      disabled={available.length === 0}
      leading={pills.length > 0 ? pills : undefined}
      onChange={(id) => onChange([...value, id])}
    />
  )
}
