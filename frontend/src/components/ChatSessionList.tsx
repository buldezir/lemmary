import { type SubmitEvent, useState } from 'react'
import { Button, inputClassName } from './ui'
import { chatSessionDateLabel, chatSessionTitle, type ChatSession } from '../lib/api/chats'

function PencilIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-3.5 w-3.5"
      aria-hidden="true"
    >
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </svg>
  )
}

function TrashIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-3.5 w-3.5"
      aria-hidden="true"
    >
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
    </svg>
  )
}

type ChatSessionRowProps = {
  session: ChatSession
  active: boolean
  busy: boolean
  compact: boolean
  onSelect: (session: ChatSession) => void
  onRename: (id: string, title: string) => Promise<void>
  onDelete: (session: ChatSession) => Promise<void>
}

function ChatSessionRow({
  session,
  active,
  busy,
  compact,
  onSelect,
  onRename,
  onDelete,
}: ChatSessionRowProps) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(session.title)

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const title = draft.trim()
    if (!title) {
      return
    }
    await onRename(session.id, title)
    setEditing(false)
  }

  if (editing) {
    return (
      <li className="rounded-xs border border-line bg-bright px-2 py-2">
        <form className="flex flex-col gap-2" onSubmit={onSubmit}>
          <input
            aria-label="Chat title"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            className={inputClassName}
          />
          <div className="flex items-center gap-2">
            <Button type="submit" size="xs" disabled={busy}>
              Save
            </Button>
            <Button
              size="xs"
              variant="secondary"
              disabled={busy}
              onClick={() => {
                setDraft(session.title)
                setEditing(false)
              }}
            >
              Cancel
            </Button>
          </div>
        </form>
      </li>
    )
  }

  return (
    <li
      className={`flex items-center justify-between gap-1 rounded-xs border px-2 py-2 ${
        active ? 'border-oxblood bg-wash' : 'border-line bg-bright'
      }`}
    >
      <button
        type="button"
        aria-current={active ? 'page' : undefined}
        title={chatSessionTitle(session)}
        onClick={() => onSelect(session)}
        className="min-w-0 flex-1 text-left"
      >
        <span className="block truncate text-sm font-medium text-ink">
          {chatSessionTitle(session)}
        </span>
        {!compact && (
          <span className="block font-mono text-xs tabular-nums text-ink-soft">
            {chatSessionDateLabel(session.last_message_at)}
          </span>
        )}
      </button>
      <div className="flex shrink-0 items-center gap-0.5">
        <button
          type="button"
          aria-label="Rename chat"
          title="Rename chat"
          disabled={busy}
          onClick={() => {
            setDraft(session.title)
            setEditing(true)
          }}
          className="p-1 text-ink-soft transition-colors hover:text-oxblood disabled:opacity-50"
        >
          <PencilIcon />
        </button>
        <button
          type="button"
          aria-label="Delete chat"
          title="Delete chat"
          disabled={busy}
          onClick={() => void onDelete(session)}
          className="p-1 text-ink-soft transition-colors hover:text-madder disabled:opacity-50"
        >
          <TrashIcon />
        </button>
      </div>
    </li>
  )
}

export type ChatSessionListProps = {
  sessions: ChatSession[]
  activeSessionId?: string
  loading: boolean
  error?: string
  /** Drops the per-row date and caps the height, for the document page's rail. */
  compact?: boolean
  busy?: boolean
  /** True when the current chat is empty and unsaved, so "New chat" is a no-op. */
  newChatDisabled?: boolean
  onSelect: (session: ChatSession) => void
  onNewChat: () => void
  onRename: (id: string, title: string) => Promise<void>
  onDelete: (session: ChatSession) => Promise<void>
}

/**
 * The saved-conversation rail.
 *
 * Rows are buttons rather than `Link`s and navigation happens in the caller:
 * TanStack types `to`/`params` against the route tree, and the two call sites
 * sit in different subtrees, so linking here would need the same `as never`
 * cast EditionMenuItem already carries. The trade is losing middle-click on a
 * row, which is a fair price for keeping every other Link in the app checked.
 */
export function ChatSessionList({
  sessions,
  activeSessionId,
  loading,
  error,
  compact = false,
  busy = false,
  newChatDisabled = false,
  onSelect,
  onNewChat,
  onRename,
  onDelete,
}: ChatSessionListProps) {
  return (
    <nav aria-label="Chat sessions" className="flex flex-col gap-2">
      <Button
        variant="secondary"
        size="sm"
        disabled={busy || newChatDisabled}
        onClick={onNewChat}
        className="w-full"
      >
        New chat
      </Button>
      {error && <p className="text-sm text-madder">{error}</p>}
      {loading && sessions.length === 0 && <p className="text-sm text-ink-soft">Loading...</p>}
      {!loading && sessions.length === 0 && (
        <p className="text-sm text-ink-faint">No chats yet.</p>
      )}
      {sessions.length > 0 && (
        <ul className={`flex flex-col gap-1.5 ${compact ? 'max-h-64 overflow-y-auto' : ''}`}>
          {sessions.map((session) => (
            <ChatSessionRow
              key={session.id}
              session={session}
              active={session.id === activeSessionId}
              busy={busy}
              compact={compact}
              onSelect={onSelect}
              onRename={onRename}
              onDelete={onDelete}
            />
          ))}
        </ul>
      )}
    </nav>
  )
}
