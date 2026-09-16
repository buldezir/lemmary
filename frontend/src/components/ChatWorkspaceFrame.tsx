import type { ReactNode } from 'react'

import { Button } from './ui'
import { ChatSessionList } from './ChatSessionList'
import type { ChatSession } from '../lib/api/chats'

/**
 * The furniture around a chat: the heading, the rail of saved conversations,
 * and the column the page fills. Both chat pages draw the same frame; what
 * goes inside it is where they part company.
 */
export function ChatWorkspaceFrame({
  title,
  hint,
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
  title: string
  hint: string
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
      <div>
        <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">{title}</h2>
        <p className="text-sm text-ink-soft">{hint} Chats are saved.</p>
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
