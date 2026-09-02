import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'

export type ComboboxOption = {
  value: string
  label: string
  /** Kept in the list whatever the query is — for actions like "Custom model id…". */
  pinned?: boolean
}

type Props = {
  value: string
  options: ComboboxOption[]
  onChange: (value: string) => void
  /** Shown when nothing is selected, and as the input's placeholder. */
  placeholder?: string
  /** Ties the input to a caller-owned <label htmlFor>. */
  id?: string
  ariaLabel?: string
  disabled?: boolean
  /** Renders a disabled input with a waiting placeholder instead of a list. */
  loading?: boolean
  loadingLabel?: string
  className?: string
  /** Background utility for the input, so it can match the surface it sits on. */
  bgClassName?: string
}

const comboboxInputClassName =
  'w-full rounded-xs border border-line-strong py-2 pr-8 pl-3 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood disabled:cursor-not-allowed disabled:opacity-50'

function normalize(value: string) {
  return value.trim().toLowerCase()
}

/**
 * A select rendered as a text input plus a filtered listbox, so long option
 * lists (model catalogues, tag lists) stay searchable.
 */
export function Combobox({
  value,
  options,
  onChange,
  placeholder = '',
  id,
  ariaLabel,
  disabled = false,
  loading = false,
  loadingLabel = 'Loading…',
  className = '',
  bgClassName = 'bg-bright',
}: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const highlightedRef = useRef<HTMLLIElement>(null)
  const [open, setOpen] = useState(false)
  // null means "showing the selection", a string means the user is typing.
  const [query, setQuery] = useState<string | null>(null)
  const [highlightedIndex, setHighlightedIndex] = useState(0)

  const selectedLabel = options.find((option) => option.value === value)?.label ?? ''

  const filteredOptions = useMemo(() => {
    const needle = normalize(query ?? '')
    if (!needle) return options
    return options.filter(
      (option) =>
        option.pinned ||
        normalize(option.label).includes(needle) ||
        normalize(option.value).includes(needle),
    )
  }, [options, query])

  const lastIndex = Math.max(filteredOptions.length - 1, 0)
  const activeIndex = Math.min(highlightedIndex, lastIndex)

  function close() {
    setOpen(false)
    setQuery(null)
    setHighlightedIndex(0)
  }

  function openList() {
    if (disabled || loading) return
    setOpen(true)
    // Start on the current selection so Enter is a no-op rather than a surprise.
    const selectedIndex = options.findIndex((option) => option.value === value)
    setHighlightedIndex(selectedIndex < 0 ? 0 : selectedIndex)
  }

  function selectOption(nextValue: string) {
    onChange(nextValue)
    close()
  }

  useEffect(() => {
    if (!open) return
    highlightedRef.current?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex, open, filteredOptions])

  useEffect(() => {
    if (!open) return

    function onPointerDown(event: MouseEvent) {
      if (!rootRef.current?.contains(event.target as Node)) {
        close()
      }
    }

    document.addEventListener('mousedown', onPointerDown)
    return () => document.removeEventListener('mousedown', onPointerDown)
  }, [open])

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Escape') {
      event.preventDefault()
      close()
      return
    }

    if (event.key === 'ArrowDown') {
      event.preventDefault()
      if (!open) {
        openList()
        return
      }
      setHighlightedIndex(Math.min(activeIndex + 1, lastIndex))
      return
    }

    if (event.key === 'ArrowUp') {
      event.preventDefault()
      if (!open) {
        openList()
        return
      }
      setHighlightedIndex(Math.max(activeIndex - 1, 0))
      return
    }

    if (event.key === 'Enter' && open) {
      event.preventDefault()
      const option = filteredOptions[activeIndex]
      if (option) {
        selectOption(option.value)
      }
      return
    }

    if (event.key === 'Tab' && open) {
      close()
    }
  }

  return (
    <div className={`relative ${className}`} ref={rootRef}>
      <input
        id={id}
        type="text"
        role="combobox"
        aria-label={ariaLabel}
        aria-expanded={open}
        aria-autocomplete="list"
        aria-activedescendant={open ? `${id ?? 'combobox'}-option-${activeIndex}` : undefined}
        autoComplete="off"
        spellCheck={false}
        disabled={disabled || loading}
        placeholder={loading ? loadingLabel : selectedLabel || placeholder}
        value={loading ? '' : open && query !== null ? query : selectedLabel}
        onChange={(event) => {
          setQuery(event.target.value)
          setOpen(true)
          setHighlightedIndex(0)
        }}
        onFocus={(event) => {
          openList()
          event.currentTarget.select()
        }}
        onClick={() => openList()}
        onKeyDown={onKeyDown}
        className={`${comboboxInputClassName} ${bgClassName}`}
      />
      <span
        className="pointer-events-none absolute inset-y-0 right-2 flex items-center text-ink-faint"
        aria-hidden="true"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          viewBox="0 0 20 20"
          fill="currentColor"
          className={`h-4 w-4 transition-transform ${open ? 'rotate-180' : ''}`}
        >
          <path
            fillRule="evenodd"
            d="M5.23 7.21a.75.75 0 0 1 1.06.02L10 10.94l3.71-3.71a.75.75 0 1 1 1.06 1.06l-4.24 4.24a.75.75 0 0 1-1.06 0L5.21 8.29a.75.75 0 0 1 .02-1.08Z"
            clipRule="evenodd"
          />
        </svg>
      </span>
      {open && (
        <ul
          role="listbox"
          className="absolute z-20 mt-1 max-h-56 w-full overflow-auto rounded-xs border border-line bg-surface py-1 shadow-sm"
        >
          {filteredOptions.length === 0 ? (
            <li className="px-3 py-1.5 text-sm text-ink-faint">No matches</li>
          ) : (
            filteredOptions.map((option, index) => {
              const selected = option.value === value
              const highlighted = index === activeIndex
              return (
                <li
                  key={`${option.value}-${option.label}`}
                  id={`${id ?? 'combobox'}-option-${index}`}
                  ref={highlighted ? highlightedRef : undefined}
                  role="option"
                  aria-selected={selected}
                  className={`cursor-pointer truncate px-3 py-1.5 text-sm ${
                    highlighted ? 'bg-wash text-ink' : 'text-ink-muted'
                  } ${selected ? 'font-medium' : ''}`}
                  title={option.label}
                  onMouseEnter={() => setHighlightedIndex(index)}
                  onMouseDown={(event) => {
                    event.preventDefault()
                    selectOption(option.value)
                  }}
                >
                  {option.label}
                </li>
              )
            })
          )}
        </ul>
      )}
    </div>
  )
}
