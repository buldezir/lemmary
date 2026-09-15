import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'

import { Button } from './ui'
import { ChatSessionList } from './ChatSessionList'
import type { ChatSession } from '../lib/api/chats'
import type { SearchMode } from '../lib/api/ai'

/**
 * The furniture around a chat: the heading, the mode links, the rail of saved
 * conversations, and the column the page fills. Both chat pages draw the same
 * frame; what goes inside it is where they part company.
 */
export function ChatWorkspaceFrame({
  mode,
  hint,
  locked,
  rows,
  sessionId,
  sessionsLoading,
  sessionsError,
  railOpen,
  onToggleRail,
  railBusy,
  railError,
  newChatDisabled,
  onSelect,
  onNewChat,
  onRename,
  onDelete,
  children,
}: {
  mode: SearchMode
  hint: string
  locked: boolean
  rows: ChatSession[]
  sessionId: string | undefined
  sessionsLoading: boolean
  sessionsError: string
  railOpen: boolean
  onToggleRail: () => void
  railBusy: boolean
  railError: string
  newChatDisabled: boolean
  onSelect: (session: ChatSession) => void
  onNewChat: () => void
  onRename: (id: string, title: string) => Promise<void>
  onDelete: (session: ChatSession) => Promise<void>
  children: ReactNode
}) {
  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">Deep Search</h2>
          <p className="text-sm text-ink-soft">
            {hint} {locked ? 'A chat stays in the mode it started in.' : 'Chats are saved.'}
          </p>
        </div>
        {/* Links rather than a radiogroup, so back, bookmark and open-in-new-tab
            work on them. They stop being links once the chat exists: see the
            mode lock above. */}
        <ModeSwitch mode={mode} locked={locked} />
      </div>

      <Button
        variant="secondary"
        size="sm"
        aria-expanded={railOpen}
        onClick={onToggleRail}
        className="self-start lg:hidden"
      >
        Chats ({rows.length})
      </Button>

      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:gap-3">
        {/* One instance across breakpoints, toggled by class: two would put the
            rows and their rename inputs into the accessibility tree twice. */}
        <aside className={`${railOpen ? 'block' : 'hidden'} lg:block lg:w-60 lg:shrink-0`}>
          <ChatSessionList
            sessions={rows}
            activeSessionId={sessionId}
            loading={sessionsLoading}
            error={railError || sessionsError}
            busy={railBusy}
            newChatDisabled={newChatDisabled}
            onSelect={onSelect}
            onNewChat={onNewChat}
            onRename={onRename}
            onDelete={onDelete}
          />
        </aside>

        {/* min-w-0: without it a wide code block or an unbroken token in a
            markdown reply stretches this column past the page's max width. */}
        <div className="min-w-0 flex-1">{children}</div>
      </div>
    </section>
  )
}

const modes: { value: SearchMode; label: string; to: '/rag/search' | '/rag/research' }[] = [
  { value: 'search', label: 'Search', to: '/rag/search' },
  { value: 'research', label: 'Research', to: '/rag/research' },
]

export const modeHints: Record<SearchMode, string> = {
  search: 'Find documents and list them.',
  research: 'Read the documents and answer, with citations.',
}

function ModeSwitch({ mode, locked }: { mode: SearchMode; locked: boolean }) {
  const className = (item: (typeof modes)[number]) =>
    `px-3 py-1.5 text-sm transition-colors ${
      mode === item.value ? 'bg-ink text-paper' : 'text-ink-muted'
    }`

  if (locked) {
    return (
      <div
        role="group"
        aria-label="Search mode"
        title="A chat stays in the mode it started in. Start a new chat to switch."
        className="flex rounded-xs border border-line bg-surface p-1"
      >
        {modes.map((item) => (
          <span
            key={item.value}
            aria-current={mode === item.value ? 'true' : undefined}
            className={`${className(item)} ${mode === item.value ? '' : 'opacity-40'}`}
          >
            {item.label}
          </span>
        ))}
      </div>
    )
  }

  return (
    <nav aria-label="Search mode" className="flex rounded-xs border border-line bg-surface p-1">
      {modes.map((item) => (
        <Link
          key={item.value}
          to={item.to}
          aria-current={mode === item.value ? 'page' : undefined}
          className={`${className(item)} ${mode === item.value ? '' : 'hover:text-ink'}`}
        >
          {item.label}
        </Link>
      ))}
    </nav>
  )
}
