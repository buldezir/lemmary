import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import type { ComboboxOption } from './Combobox'
import { listboxClassName, listOptionClassName } from './ui'

/**
 * A select whose trigger is an icon button, for a setting that does not earn a
 * field's width. The list is the Combobox's. Anything but the first option
 * marks the button, so a narrowed list does not hide behind a bare icon.
 */
export function IconSelect({
  label,
  icon,
  value,
  options,
  disabled = false,
  className = '',
  onChange,
}: {
  label: string
  /** An SVG path on a 20x20 stroked grid. */
  icon: string
  value: string
  options: ComboboxOption[]
  disabled?: boolean
  className?: string
  onChange: (value: string) => void
}) {
  const listId = useId()
  const rootRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [highlighted, setHighlighted] = useState(0)
  // Opens toward the wider side: a button near the left edge, as on a phone
  // where the header wraps, would push a right-aligned list off screen.
  const [alignLeft, setAlignLeft] = useState(false)
  const selectedIndex = options.findIndex((option) => option.value === value)
  const selected = options[selectedIndex]

  useEffect(() => {
    if (!open) return
    function onPointerDown(event: MouseEvent) {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onPointerDown)
    return () => document.removeEventListener('mousedown', onPointerDown)
  }, [open])

  function openList() {
    const rect = rootRef.current?.getBoundingClientRect()
    if (rect) setAlignLeft(rect.left + rect.width / 2 < window.innerWidth / 2)
    setHighlighted(Math.max(selectedIndex, 0))
    setOpen(true)
  }

  function pick(next: string) {
    setOpen(false)
    if (next !== value) onChange(next)
  }

  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    if (event.key === 'Escape' && open) {
      event.preventDefault()
      setOpen(false)
    } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      if (!open) return openList()
      const step = event.key === 'ArrowDown' ? 1 : -1
      setHighlighted((index) => Math.min(Math.max(index + step, 0), options.length - 1))
    } else if ((event.key === 'Enter' || event.key === ' ') && open) {
      event.preventDefault()
      pick(options[highlighted].value)
    } else if (event.key === 'Tab') {
      setOpen(false)
    }
  }

  return (
    <div className={`relative shrink-0 ${className}`} ref={rootRef}>
      <button
        type="button"
        aria-label={label}
        title={selected ? `${label}: ${selected.label}` : label}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open ? `${listId}-${highlighted}` : undefined}
        disabled={disabled}
        onClick={() => (open ? setOpen(false) : openList())}
        onKeyDown={onKeyDown}
        className={`flex items-center justify-center rounded-xs border bg-surface px-2.5 py-2 transition-colors hover:bg-bright hover:text-ink focus:border-oxblood focus:ring-1 focus:ring-oxblood focus:outline-none disabled:cursor-not-allowed disabled:opacity-50 ${
          selectedIndex > 0 ? 'border-oxblood text-oxblood' : 'border-line-strong text-ink-muted'
        }`}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          viewBox="0 0 20 20"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="h-5 w-5"
          aria-hidden="true"
        >
          <path d={icon} />
        </svg>
      </button>
      {open && (
        <ul id={listId} role="listbox" aria-label={label} className={`${listboxClassName} ${alignLeft ? 'left-0' : 'right-0'} mt-1 w-max min-w-48`}>
          {options.map((option, index) => (
            <li
              key={option.value}
              id={`${listId}-${index}`}
              role="option"
              aria-selected={option.value === value}
              className={`${listOptionClassName} ${
                index === highlighted ? 'bg-wash text-ink' : 'text-ink-muted'
              } ${option.value === value ? 'font-medium' : ''}`}
              onMouseEnter={() => setHighlighted(index)}
              onMouseDown={(event) => {
                event.preventDefault()
                pick(option.value)
              }}
            >
              {option.label}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
