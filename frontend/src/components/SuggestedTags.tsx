/**
 * The AI's proposed new tags on a document awaiting review, each a button that
 * accepts it. Dashed, unlike a real tag chip: nothing here exists yet.
 */
export function SuggestedTags({
  names,
  disabled,
  onAccept,
  className = '',
}: {
  names: string[]
  disabled?: boolean
  onAccept: (name: string) => void
  className?: string
}) {
  if (names.length === 0) return null
  return (
    <div className={`flex flex-wrap items-center gap-1.5 ${className}`}>
      <span className="text-[11px] uppercase tracking-[0.08em] text-amber-800">Suggested</span>
      {names.map((name) => (
        <button
          key={name}
          type="button"
          disabled={disabled}
          aria-label={`Add tag ${name}`}
          title="Create this tag and add it to the document"
          className="border border-dashed border-amber-800 px-1.5 py-0.5 text-[11px] text-amber-800 transition-colors hover:bg-amber-800 hover:text-paper disabled:opacity-50"
          onClick={() => onAccept(name)}
        >
          + {name}
        </button>
      ))}
    </div>
  )
}
