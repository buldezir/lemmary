import { SHARED_TAG_ID, SHARED_TAG_NAME } from '../lib/api/documents'
import type { TagRecord } from '../lib/api/tags'
import { Combobox } from './Combobox'

const NO_TAGS = '__none__'

/**
 * Narrows the list to documents carrying every chosen tag, or to those with
 * none. The two exclude each other, so choosing one clears the other. "shared"
 * rides along as an option rather than a second control, means "owned by
 * someone else", and combines with either.
 */
export function TagFilter({
  value,
  untagged,
  options,
  onChange,
}: {
  value: string[]
  untagged: boolean
  options: TagRecord[]
  onChange: (next: string[], untagged: boolean) => void
}) {
  const byId = new Map(options.map((tag) => [tag.id, tag]))
  const shared = value.includes(SHARED_TAG_ID)
  const tags = value.filter((id) => id !== SHARED_TAG_ID)
  const scope = shared ? [SHARED_TAG_ID] : []
  const available = options.filter((tag) => !value.includes(tag.id))

  const chosen = [
    ...(shared
      ? [{ id: SHARED_TAG_ID, name: SHARED_TAG_NAME, color: undefined, next: tags, nextUntagged: untagged }]
      : []),
    ...(untagged
      ? [{ id: NO_TAGS, name: 'No tags', color: undefined, next: scope, nextUntagged: false }]
      : tags.map((id) => ({
          id,
          // An id with no name behind it is a tag deleted since the link was
          // made; it still has to be removable.
          name: byId.get(id)?.name ?? 'Unknown tag',
          color: byId.get(id)?.color,
          next: value.filter((other) => other !== id),
          nextUntagged: false,
        }))),
  ]

  const pills = chosen.map(({ id, name, color, next, nextUntagged }) => (
    <span
      key={id}
      className="flex max-w-full items-center gap-1 rounded-xs border border-line-strong bg-wash px-2 py-0.5 text-xs text-ink"
      style={{ borderColor: color || undefined }}
    >
      <span className="truncate">{name}</span>
      <button
        type="button"
        aria-label={`Remove ${name}`}
        className="shrink-0 text-ink-faint transition-colors hover:text-madder"
        onClick={() => onChange(next, nextUntagged)}
      >
        &times;
      </button>
    </span>
  ))

  const choices = [
    ...(shared ? [] : [{ value: SHARED_TAG_ID, label: SHARED_TAG_NAME }]),
    ...(untagged || tags.length > 0 ? [] : [{ value: NO_TAGS, label: 'No tags' }]),
    ...available.map((tag) => ({ value: tag.id, label: tag.name })),
  ]

  return (
    <Combobox
      value=""
      options={choices}
      placeholder={untagged ? 'Or pick a tag...' : value.length > 0 ? 'Add a tag...' : 'All tags'}
      ariaLabel="Filter by tag"
      bgClassName="bg-surface"
      className="w-full min-w-0 sm:flex-1"
      disabled={choices.length === 0}
      leading={pills.length > 0 ? pills : undefined}
      onChange={(id) => {
        if (id === NO_TAGS) onChange(scope, true)
        else if (id === SHARED_TAG_ID) onChange([...value, id], untagged)
        else onChange([...value, id], false)
      }}
    />
  )
}
