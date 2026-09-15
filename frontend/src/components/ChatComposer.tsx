import { type SubmitEvent } from 'react'
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
  /**
   * A second way to send what is in the box, beside the main one and under the
   * same enablement rule. Absent on pages that have only one.
   */
  secondary?: { label: string; title?: string; onClick: () => void }
}

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
  secondary,
}: ChatComposerProps) {
  function handleSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    onSubmit()
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
          autoFocus={autoFocus}
          disabled={sending || disabled}
          placeholder={placeholder}
          className="min-h-12 w-0 min-w-40 flex-1 resize-y rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood disabled:cursor-not-allowed disabled:opacity-50"
        />
        <Button type="submit" disabled={sending || disabled || !value.trim()}>
          {sending ? sendingLabel : submitLabel}
        </Button>
        {secondary && (
          <Button
            type="button"
            variant="secondary"
            title={secondary.title}
            disabled={sending || disabled || !value.trim()}
            onClick={secondary.onClick}
          >
            {secondary.label}
          </Button>
        )}
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
