import { createContext, useContext, useState, type ComponentProps, type ReactNode } from 'react'
import type { Components, ExtraProps, Options } from 'react-markdown'
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown'
import rehypeRaw from 'rehype-raw'
import rehypeSanitize, { defaultSchema } from 'rehype-sanitize'
import remarkGfm from 'remark-gfm'
import { Link } from '@tanstack/react-router'
import { t } from '../i18n'
import { downloadChatReport } from '../lib/api/chats'
import { parseChart } from '../lib/chart'
import { documentLinkTarget } from '../lib/documentLink'
import { saveBlob } from '../lib/download'
import { Chart } from './Chart'

/** The conversation the content belongs to: where the reports it links to are fetched from. */
const ChatContext = createContext<string | undefined>(undefined)

function MarkdownLink({ href, children }: ComponentProps<'a'>) {
  const chatId = useContext(ChatContext)
  const documentId = documentLinkTarget(href)
  if (documentId) {
    return (
      <Link to="/document/$documentId" params={{ documentId }} className="underline hover:text-oxblood">
        {children}
      </Link>
    )
  }
  if (href?.startsWith('pdf:')) {
    return chatId ? (
      <ReportLink chatId={chatId} reportId={href.slice(4)}>
        {children}
      </ReportLink>
    ) : (
      <>{children}</>
    )
  }
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="underline hover:text-oxblood">
      {children}
    </a>
  )
}

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
  a: MarkdownLink,
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
  th: ({ children, colSpan, rowSpan, style, scope }) => (
    <th colSpan={colSpan} rowSpan={rowSpan} style={style} scope={scope} className="px-2 py-1 font-semibold">
      {children}
    </th>
  ),
  td: ({ children, colSpan, rowSpan, style, headers }) => (
    <td colSpan={colSpan} rowSpan={rowSpan} style={style} headers={headers} className="border-t border-line px-2 py-1">
      {children}
    </td>
  ),
  pre: ({ node, children }) => {
    const chart = parseChart(chartSource(node) ?? '')
    if (chart) return <Chart spec={chart} />
    return <pre className="mb-2 overflow-x-auto rounded-xs bg-wash p-3 text-xs last:mb-0">{children}</pre>
  },
  // A chart the answer places is only a reference until the server swaps the
  // chart in, which happens once the answer is complete.
  img: ({ src, alt }) =>
    typeof src === 'string' && src.startsWith('chart:') ? (
      <span className="block text-xs text-ink-faint">{t('chart.drawing', { title: alt ?? '' })}</span>
    ) : (
      <img src={src} alt={alt} />
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

/** The text of a ```chart block, which react-markdown hands over as a code element in a pre. */
function chartSource(node: ExtraProps['node']): string | null {
  const code = node?.children[0]
  if (code?.type !== 'element' || code.tagName !== 'code') return null
  const classes = code.properties.className
  if (!Array.isArray(classes) || !classes.includes('language-chart')) return null
  const text = code.children[0]
  return text?.type === 'text' ? text.value : null
}

const artifactRef = /^(chart|pdf):[a-z0-9]+$/

function urlTransform(url: string) {
  return artifactRef.test(url) ? url : defaultUrlTransform(url)
}

/**
 * A report the answer links to. Fetched rather than linked because the route
 * needs the session token, which a plain link cannot carry.
 */
function ReportLink({ chatId, reportId, children }: { chatId: string; reportId: string; children: ReactNode }) {
  const [state, setState] = useState<'idle' | 'busy' | 'failed'>('idle')
  const name = typeof children === 'string' ? children : 'report.pdf'
  const download = async () => {
    setState('busy')
    try {
      saveBlob(await downloadChatReport(chatId, reportId), name.endsWith('.pdf') ? name : `${name}.pdf`)
      setState('idle')
    } catch {
      setState('failed')
    }
  }
  return (
    <span className="my-2 flex flex-col gap-1 border-l-2 border-oxblood bg-bright px-3 py-2 font-medium">
      <button
        type="button"
        className="self-start text-left text-oxblood underline underline-offset-4 hover:text-ink disabled:opacity-60"
        disabled={state === 'busy'}
        onClick={() => void download()}
      >
        <span aria-hidden="true" className="inline-block w-4">
          ↓
        </span>
        {children}
      </button>
      {state === 'failed' && <span className="text-xs font-normal text-madder">{t('chats.reportFailed')}</span>}
    </span>
  )
}

// Embedded HTML is untrusted, and an image would make the viewer's browser
// fetch whatever URL it names, so images are dropped along with scripts.
const htmlSchema = {
  ...defaultSchema,
  tagNames: defaultSchema.tagNames?.filter((tag) => !['img', 'picture', 'source'].includes(tag)),
}
const htmlPlugins: Options['rehypePlugins'] = [rehypeRaw, [rehypeSanitize, htmlSchema]]

/** Without chatId a report link is plain text. `html` renders embedded HTML, sanitized. */
export function MarkdownContent({ content, chatId, html }: { content: string; chatId?: string; html?: boolean }) {
  return (
    <ChatContext value={chatId}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={html ? htmlPlugins : undefined}
        components={components}
        urlTransform={urlTransform}
      >
        {content}
      </ReactMarkdown>
    </ChatContext>
  )
}
