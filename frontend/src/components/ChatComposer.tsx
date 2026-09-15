import { type KeyboardEvent, type SubmitEvent } from 'react'
import { Button } from './ui'

type ChatComposerProps = {
  value: string
  onChange: (value: string) => void
  onSubmit: () => void
  /** Placeholder and labels stay per-page: the browser suites select on them. */
  placeholder: string
  submitLabel: string
  sendingLabel: string
  sending: boolean
  disabled?: boolean
  error?: string
  autoFocus?: boolean
  /**
   * Offers a Cancel button while a reply is in flight. Only for a send with no
   * useful upper bound on how long it can run.
   */
  onCancel?: () => void
}

// Named for the keyboard the reader has. Read once: nothing about it changes
// while the page is open, and it labels a button that rerenders on every
// keystroke.
const sendChord =
  typeof navigator !== 'undefined' && /Mac|iP(hone|ad|od)/.test(navigator.userAgent)
    ? '⌘ + Enter'
    : 'Ctrl + Enter'

export function ChatComposer({
  value,
  onChange,
  onSubmit,
  placeholder,
  submitLabel,
  sendingLabel,
  sending,
  disabled = false,
  error,
  autoFocus = false,
  onCancel,
}: ChatComposerProps) {
  const ready = !sending && !disabled && value.trim() !== ''

  function handleSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    onSubmit()
  }

  // The chord rather than Enter alone: the box is two rows and resizable
  // because these questions run long, so a bare Enter belongs to the text.
  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== 'Enter' || !(event.metaKey || event.ctrlKey)) {
      return
    }
    event.preventDefault()
    if (ready) {
      onSubmit()
    }
  }

  return (
    <form onSubmit={handleSubmit} className="border-t border-line bg-paper/70 p-4">
      {/* Wraps because a page can put two buttons here: at phone width the
          textarea keeps its floor and the buttons take a line of their own. */}
      <div className="flex flex-wrap items-end gap-3">
        <textarea
          rows={2}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={handleKeyDown}
          autoFocus={autoFocus}
          disabled={sending || disabled}
          placeholder={placeholder}
          className="min-h-12 w-0 min-w-40 flex-1 resize-y rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood disabled:cursor-not-allowed disabled:opacity-50"
        />
        {/* Under the button rather than beside it, so the hint cannot push the
            row into a second line at phone width. */}
        <div className="flex flex-col items-stretch gap-1">
          <Button type="submit" disabled={!ready}>
            {sending ? sendingLabel : submitLabel}
          </Button>
          <span className="text-center text-xs text-ink-faint">{sendChord}</span>
        </div>
        {sending && onCancel && (
          <Button type="button" variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
        )}
      </div>
      {error && <p className="mt-2 text-sm text-madder">{error}</p>}
    </form>
  )
}
