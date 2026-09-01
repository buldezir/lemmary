import type { Components } from 'react-markdown'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Link } from '@tanstack/react-router'
import { documentLinkTarget } from '../lib/documentLink'

const components: Components = {
  p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
  ul: ({ children }) => <ul className="mb-2 list-disc space-y-1 pl-5 last:mb-0">{children}</ul>,
  ol: ({ children }) => <ol className="mb-2 list-decimal space-y-1 pl-5 last:mb-0">{children}</ol>,
  li: ({ children }) => <li>{children}</li>,
  blockquote: ({ children }) => (
    <blockquote className="mb-2 border-l-2 border-line-strong pl-3 text-ink-muted last:mb-0">
      {children}
    </blockquote>
  ),
  a: ({ href, children }) => {
    const documentId = documentLinkTarget(href)
    if (documentId) {
      return (
        <Link
          to="/document/$documentId"
          params={{ documentId }}
          className="underline hover:text-oxblood"
        >
          {children}
        </Link>
      )
    }
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" className="underline hover:text-oxblood">
        {children}
      </a>
    )
  },
  strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
  h1: ({ children }) => <h1 className="mb-2 text-base font-semibold last:mb-0">{children}</h1>,
  h2: ({ children }) => <h2 className="mb-2 text-base font-semibold last:mb-0">{children}</h2>,
  h3: ({ children }) => <h3 className="mb-2 text-sm font-semibold last:mb-0">{children}</h3>,
  hr: () => <hr className="my-3 border-line" />,
  table: ({ children }) => (
    <div className="mb-2 overflow-x-auto last:mb-0">
      <table className="min-w-full border-collapse text-left text-xs">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead className="border-b border-line-strong">{children}</thead>,
  th: ({ children }) => <th className="px-2 py-1 font-semibold">{children}</th>,
  td: ({ children }) => <td className="border-t border-line px-2 py-1">{children}</td>,
  pre: ({ children }) => (
    <pre className="mb-2 overflow-x-auto rounded-xs bg-wash p-3 text-xs last:mb-0">{children}</pre>
  ),
  code: ({ className, children, ...props }) => {
    const isBlock = Boolean(className)
    if (isBlock) {
      return (
        <code className={className} {...props}>
          {children}
        </code>
      )
    }
    return (
      <code className="rounded bg-wash px-1 py-0.5 text-xs" {...props}>
        {children}
      </code>
    )
  },
}

export function MarkdownContent({ content }: { content: string }) {
  return (
    <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
      {content}
    </ReactMarkdown>
  )
}
