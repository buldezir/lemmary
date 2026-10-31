import { useEffect, useId, useRef, useState, type KeyboardEvent, type SubmitEvent } from 'react'
import { t } from '../i18n'
import { findDocumentsToMention } from '../lib/api/documents'
import {
  attachDocuments,
  mentionTitle,
  splitAttachments,
  type DocumentMention,
} from '../lib/documentLink'
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
   * Lets a word starting with @ attach a document. The attached documents are
   * kept in the value, as links after the question, and shown as pills.
   */
  attachable?: boolean
}

// Named for the keyboard the reader has. Read once: nothing about it changes
// while the page is open, and it labels a button that rerenders on every
// keystroke.
const sendChord =
  typeof navigator !== 'undefined' && /Mac|iP(hone|ad|od)/.test(navigator.userAgent)
    ? '⌘ + Enter'
    : t('chatComposer.ctrlEnter')

/** The @word the caret ends, when the @ starts a word. */
const mentionPattern = /(?:^|\s)@(\S*)$/

type Mention = { start: number; query: string }

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
  attachable = false,
}: ChatComposerProps) {
  const { question, documents } = attachable
    ? splitAttachments(value)
    : { question: value, documents: [] }
  const ready = !sending && !disabled && question.trim() !== ''
  const listId = useId()
  const textRef = useRef<HTMLTextAreaElement>(null)
  const [mention, setMention] = useState<Mention | null>(null)
  const [active, setActive] = useState(0)
  const found = useMentionSearch(mention?.query)?.filter(
    (doc) => !documents.some((attached) => attached.id === doc.id),
  )

  function handleSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    onSubmit()
  }

  function trackMention(text: string, caret: number) {
    if (!attachable) return
    const match = mentionPattern.exec(text.slice(0, caret))
    const next = match ? { start: caret - match[1].length - 1, query: match[1] } : null
    if (next?.start === mention?.start && next?.query === mention?.query) return
    setMention(next)
    setActive(0)
  }

  function attach(doc: DocumentMention) {
    if (!mention) return
    const title = mentionTitle(doc.title) || t('common.untitledDocument')
    const end = mention.start + 1 + mention.query.length
    const next = question.slice(0, mention.start) + question.slice(end)
    // On the node before the rerender, which then finds the value unchanged and
    // leaves the caret be: placed any later, keys typed meanwhile land behind it.
    if (textRef.current) {
      textRef.current.value = next
      textRef.current.setSelectionRange(mention.start, mention.start)
    }
    onChange(attachDocuments(next, [...documents, { id: doc.id, title }]))
    setMention(null)
  }

  function detach(id: string) {
    onChange(
      attachDocuments(
        question,
        documents.filter((doc) => doc.id !== id),
      ),
    )
  }

  // The chord rather than Enter alone: the box is two rows and resizable
  // because these questions run long, so a bare Enter belongs to the text.
  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    const chord = event.metaKey || event.ctrlKey
    if (mention && found && found.length > 0 && !chord) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault()
        const step = event.key === 'ArrowDown' ? 1 : found.length - 1
        setActive((active + step) % found.length)
        return
      }
      if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault()
        attach(found[Math.min(active, found.length - 1)])
        return
      }
    }
    if (mention && event.key === 'Escape') {
      event.preventDefault()
      setMention(null)
      return
    }
    // Backspace at the very start takes the last pill, as in the tag field.
    const atStart = event.currentTarget.selectionStart === 0 && event.currentTarget.selectionEnd === 0
    if (event.key === 'Backspace' && atStart && documents.length > 0) {
      event.preventDefault()
      detach(documents[documents.length - 1].id)
      return
    }
    if (event.key !== 'Enter' || !chord) {
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
        {/* The border is on the box rather than the textarea, so the pills and
            the text read as one field, as in the tag filter. */}
        <div className="relative w-0 min-w-40 flex-1 rounded-xs border border-line-strong bg-surface focus-within:border-oxblood focus-within:ring-1 focus-within:ring-oxblood has-[textarea:disabled]:opacity-50">
          {documents.length > 0 && (
            <ul aria-label={t('chatComposer.attached')} className="flex flex-wrap gap-1.5 px-2 pt-2">
              {documents.map((doc) => (
                <li
                  key={doc.id}
                  className="flex max-w-full items-center gap-1 rounded-xs border border-line-strong bg-wash px-2 py-0.5 text-xs text-ink"
                >
                  <span className="truncate" title={doc.title}>
                    {doc.title}
                  </span>
                  <button
                    type="button"
                    aria-label={t('chatComposer.detach', { title: doc.title })}
                    disabled={sending || disabled}
                    className="shrink-0 text-ink-faint transition-colors hover:text-madder disabled:cursor-not-allowed"
                    onClick={() => detach(doc.id)}
                  >
                    &times;
                  </button>
                </li>
              ))}
            </ul>
          )}
          <textarea
            ref={textRef}
            rows={2}
            value={question}
            onChange={(event) => {
              onChange(attachDocuments(event.target.value, documents))
              trackMention(event.target.value, event.target.selectionStart)
            }}
            onSelect={(event) =>
              trackMention(event.currentTarget.value, event.currentTarget.selectionStart)
            }
            onBlur={() => setMention(null)}
            onKeyDown={handleKeyDown}
            autoFocus={autoFocus}
            disabled={sending || disabled}
            placeholder={placeholder}
            aria-controls={mention ? listId : undefined}
            aria-activedescendant={mention && found?.length ? `${listId}-${active}` : undefined}
            className="block min-h-12 w-full resize-y bg-transparent px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint disabled:cursor-not-allowed"
          />
          {mention && (
            <ul
              id={listId}
              role="listbox"
              aria-label={t('chatComposer.attach')}
              className="absolute bottom-full left-0 z-20 mb-1 max-h-56 w-full max-w-md overflow-auto rounded-xs border border-line bg-surface py-1 shadow-sm"
            >
              {found === undefined ? (
                <li className="px-3 py-1.5 text-sm text-ink-faint">{t('common.loading')}</li>
              ) : found.length === 0 ? (
                <li className="px-3 py-1.5 text-sm text-ink-faint">{t('combobox.noMatches')}</li>
              ) : (
                found.map((doc, index) => (
                  <li
                    key={doc.id}
                    id={`${listId}-${index}`}
                    role="option"
                    aria-selected={index === active}
                    title={doc.title}
                    className={`cursor-pointer truncate px-3 py-1.5 text-sm ${
                      index === active ? 'bg-wash text-ink' : 'text-ink-muted'
                    }`}
                    onMouseEnter={() => setActive(index)}
                    onMouseDown={(event) => {
                      event.preventDefault()
                      attach(doc)
                    }}
                  >
                    {doc.title || t('common.untitledDocument')}
                  </li>
                ))
              )}
            </ul>
          )}
        </div>
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
            {t('common.cancel')}
          </Button>
        )}
      </div>
      {error && <p className="mt-2 text-sm text-madder">{error}</p>}
    </form>
  )
}

/** The documents an @word matches: null while they are being looked up. */
function useMentionSearch(query: string | undefined): DocumentMention[] | null {
  const [found, setFound] = useState<{ query: string; docs: DocumentMention[] } | null>(null)
  useEffect(() => {
    if (query === undefined) return
    let live = true
    // Debounced like the Documents list's search box: a change per keystroke.
    const timer = window.setTimeout(() => {
      findDocumentsToMention(query).then(
        (docs) => live && setFound({ query, docs }),
        () => live && setFound({ query, docs: [] }),
      )
    }, 200)
    return () => {
      live = false
      window.clearTimeout(timer)
    }
  }, [query])
  return found !== null && found.query === query ? found.docs : null
}
