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
}: ChatComposerProps) {
  function handleSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    onSubmit()
  }

  return (
    <form onSubmit={handleSubmit} className="border-t border-line bg-paper/70 p-4">
      <div className="flex items-end gap-3">
        <textarea
          rows={2}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          autoFocus={autoFocus}
          disabled={sending || disabled}
          placeholder={placeholder}
          className="min-h-12 w-0 min-w-0 flex-1 resize-y rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood disabled:cursor-not-allowed disabled:opacity-50"
        />
        <Button type="submit" disabled={sending || disabled || !value.trim()}>
          {sending ? sendingLabel : submitLabel}
        </Button>
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
