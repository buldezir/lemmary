import type { ReactNode } from 'react'

/** The bordered box holding a transcript and its composer. */
export function ChatPanel({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-128 flex-col overflow-hidden rounded-none border border-line bg-surface">
      {children}
    </div>
  )
}
