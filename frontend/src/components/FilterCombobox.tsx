import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'

export type FilterComboboxOption = {
  value: string
  label: string
}

type Props = {
  label: string
  value: string
  options: FilterComboboxOption[]
  allValue?: string
  allLabel: string
  onChange: (value: string) => void
}

const inputClassName =
  'w-full rounded-md border border-stone-300 bg-stone-50 py-2 pr-8 pl-3 text-sm outline-none placeholder:text-stone-400 focus:border-gray-900 focus:ring-1 focus:ring-gray-900'

function normalize(value: string) {
  return value.trim().toLowerCase()
}

export function FilterCombobox({
  label,
  value,
  options,
  allValue = 'all',
  allLabel,
  onChange,
}: Props) {
  const inputId = useId()
  const listboxId = useId()
  const rootRef = useRef<HTMLDivElement>(null)
  const highlightedRef = useRef<HTMLLIElement>(null)
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState<string | null>(null)
  const [highlightedIndex, setHighlightedIndex] = useState(0)

  const selectedLabel =
    value === allValue ? allLabel : (options.find((option) => option.value === value)?.label ?? allLabel)

  const filteredOptions = useMemo(() => {
    const needle = normalize(query ?? '')
    const matches = needle
      ? options.filter((option) => normalize(option.label).includes(needle))
      : options
    const showAll = !needle || normalize(allLabel).includes(needle)
    return showAll ? [{ value: allValue, label: allLabel }, ...matches] : matches
  }, [allLabel, allValue, options, query])

  function close() {
    setOpen(false)
    setQuery(null)
  }

  function selectOption(nextValue: string) {
    onChange(nextValue)
    close()
  }

  useEffect(() => {
    setHighlightedIndex(0)
  }, [query, open])

  useEffect(() => {
    if (!open) return
    highlightedRef.current?.scrollIntoView({ block: 'nearest' })
  }, [highlightedIndex, open, filteredOptions])

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
        setOpen(true)
        return
      }
      setHighlightedIndex((index) => Math.min(index + 1, Math.max(filteredOptions.length - 1, 0)))
      return
    }

    if (event.key === 'ArrowUp') {
      event.preventDefault()
      if (!open) {
        setOpen(true)
        return
      }
      setHighlightedIndex((index) => Math.max(index - 1, 0))
      return
    }

    if (event.key === 'Enter' && open) {
      event.preventDefault()
      const option = filteredOptions[highlightedIndex]
      if (option) {
        selectOption(option.value)
      }
    }
  }

  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={inputId} className="text-xs font-medium text-stone-500">
        {label}
      </label>
      <div className="relative" ref={rootRef}>
        <input
          id={inputId}
          type="text"
          role="combobox"
          aria-expanded={open}
          aria-controls={listboxId}
          aria-autocomplete="list"
          aria-activedescendant={open ? `${listboxId}-option-${highlightedIndex}` : undefined}
          autoComplete="off"
          spellCheck={false}
          placeholder={selectedLabel}
          value={open && query !== null ? query : selectedLabel}
          onChange={(event) => {
            setQuery(event.target.value)
            setOpen(true)
          }}
          onFocus={(event) => {
            setOpen(true)
            event.currentTarget.select()
          }}
          onClick={() => setOpen(true)}
          onKeyDown={onKeyDown}
          className={inputClassName}
        />
        <span className="pointer-events-none absolute inset-y-0 right-2 flex items-center text-stone-400" aria-hidden="true">
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
            id={listboxId}
            role="listbox"
            className="absolute z-20 mt-1 max-h-56 w-full overflow-auto rounded-md border border-stone-200 bg-stone-50 py-1 shadow-sm"
          >
            {filteredOptions.length === 0 ? (
              <li className="px-3 py-1.5 text-sm text-stone-400">No matches</li>
            ) : (
              filteredOptions.map((option, index) => {
                const selected = option.value === value
                const highlighted = index === highlightedIndex
                return (
                  <li
                    key={`${option.value}-${option.label}`}
                    id={`${listboxId}-option-${index}`}
                    ref={highlighted ? highlightedRef : undefined}
                    role="option"
                    aria-selected={selected}
                    className={`cursor-pointer px-3 py-1.5 text-sm ${
                      highlighted ? 'bg-stone-200/80 text-stone-950' : 'text-stone-700'
                    } ${selected ? 'font-medium' : ''}`}
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
    </div>
  )
}
