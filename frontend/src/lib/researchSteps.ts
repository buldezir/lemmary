import type { ResearchStepKind } from './api/ai'

/** One visible research progress line, after folding start/progress/done events. */
export type ResearchStep = {
  kind: ResearchStepKind
  label: string
  done: boolean
}

/**
 * A research step as stored on an assistant message, matching the SSE payload
 * minus `type: "step"`.
 */
export type StoredResearchStep = {
  kind: ResearchStepKind
  status: 'start' | 'progress' | 'done'
  query?: string
  titles?: string[]
  count?: number
  done?: number
  phase?: 'screen' | 'read'
  distilled?: boolean
}

/**
 * Folds one event into the visible step list: a "start" appends a pending step,
 * the matching "done" completes it in place rather than adding a second line.
 */
export function applyStep(steps: ResearchStep[], event: StoredResearchStep) {
  if (event.status === 'start') {
    steps.push({ kind: event.kind, label: startLabel(event), done: false })
    return
  }
  const pending = [...steps].reverse().find((step) => step.kind === event.kind && !step.done)
  if (event.status === 'progress') {
    // A running count rewrites the pending line in place; a progress event
    // with nothing pending is a stray and is dropped rather than shown twice.
    if (pending) pending.label = progressLabel(event)
    return
  }
  if (!pending) {
    steps.push({ kind: event.kind, label: doneLabel(event), done: true })
    return
  }
  pending.label = doneLabel(event, pending.label)
  pending.done = true
}

/** Folds a stored trail into the labels the transcript shows. */
export function foldSteps(events: StoredResearchStep[] | undefined): ResearchStep[] | undefined {
  if (!events || events.length === 0) {
    return undefined
  }
  const steps: ResearchStep[] = []
  for (const event of events) {
    applyStep(steps, event)
  }
  return steps.map((step) => ({ ...step, done: true }))
}

function plural(n: number, noun: string) {
  return `${n} ${noun}${n === 1 ? '' : 's'}`
}

function startLabel(event: StoredResearchStep) {
  switch (event.kind) {
    case 'find':
      return event.query ? `Finding documents about “${event.query}”` : 'Finding documents'
    case 'search':
      return event.query ? `Searching “${event.query}”` : 'Searching'
    case 'read':
      return `Reading ${plural(event.count ?? 0, 'document')}`
    case 'survey':
      return event.query ? `Surveying documents for “${event.query}”` : 'Surveying documents'
    case 'count':
      return event.query ? `Counting documents matching “${event.query}”` : 'Counting documents'
    case 'web_search':
      return event.query ? `Searching the web for “${event.query}”` : 'Searching the web'
    case 'web_fetch':
      return `Reading ${plural(event.count ?? 0, 'page')}`
    case 'answer':
      return 'Writing answer'
    default:
      return `${capitalize(event.kind)} in progress`
  }
}

function progressLabel(event: StoredResearchStep) {
  const total = event.count ?? 0
  const done = event.done ?? 0
  if (event.kind === 'find') {
    const verb = event.phase === 'read' ? 'Read' : 'Screened'
    return total > 0 ? `${verb} ${done} of ${plural(total, 'document')}` : 'Finding documents'
  }
  return total > 0 ? `Surveyed ${done} of ${plural(total, 'document')}` : 'Surveying documents'
}

function capitalize(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1).replace(/_/g, ' ')
}

function doneLabel(event: StoredResearchStep, fallback?: string) {
  switch (event.kind) {
    case 'find':
    case 'search': {
      const found = `${event.count ?? 0} document${event.count === 1 ? '' : 's'} found`
      return event.query ? `“${event.query}” — ${found}` : found
    }
    case 'read': {
      const titles = event.titles ?? []
      const shown = titles.slice(0, 3).join(', ')
      const rest = titles.length > 3 ? `, and ${titles.length - 3} more` : ''
      const verb = event.distilled ? 'Read and summarised' : 'Read'
      return titles.length > 0 ? `${verb} ${shown}${rest}` : (fallback ?? `${verb} documents`)
    }
    case 'survey': {
      const surveyed = `Surveyed ${plural(event.count ?? 0, 'document')}`
      return event.query ? `${surveyed} for “${event.query}”` : surveyed
    }
    case 'count': {
      const counted = `Counted ${plural(event.count ?? 0, 'document')}`
      return event.query ? `${counted} matching “${event.query}”` : counted
    }
    case 'web_search': {
      const found = plural(event.count ?? 0, 'result')
      return event.query ? `“${event.query}” — ${found}` : `Searched the web — ${found}`
    }
    case 'web_fetch': {
      // Titles are hostnames here: a URL is too long for a step line, and the
      // page's own title is not known until it has been read.
      const hosts = event.titles ?? []
      const shown = hosts.slice(0, 3).join(', ')
      const rest = hosts.length > 3 ? `, and ${hosts.length - 3} more` : ''
      return hosts.length > 0 ? `Read ${shown}${rest}` : (fallback ?? 'Read web pages')
    }
    case 'answer':
      return 'Answer written'
    default:
      return fallback ?? `${capitalize(event.kind)} done`
  }
}
