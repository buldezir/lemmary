import { t } from '../i18n'
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

function startLabel(event: StoredResearchStep) {
  const count = event.count ?? 0
  const query = event.query
  switch (event.kind) {
    case 'search':
      return query ? t('researchSteps.searchingQuery', { query }) : t('researchSteps.searching')
    case 'read':
      return t('researchSteps.readingDocuments', { count })
    case 'survey':
      return query ? t('researchSteps.surveyingQuery', { query }) : t('researchSteps.surveying')
    case 'count':
      return query ? t('researchSteps.countingQuery', { query }) : t('researchSteps.counting')
    case 'web_search':
      return query ? t('researchSteps.searchingWebQuery', { query }) : t('researchSteps.searchingWeb')
    case 'web_fetch':
      return t('researchSteps.readingPages', { count })
    default:
      return t('researchSteps.writing')
  }
}

function progressLabel(event: StoredResearchStep) {
  const total = event.count ?? 0
  const done = event.done ?? 0
  return total > 0
    ? t('researchSteps.surveyedProgress', { done, count: total })
    : t('researchSteps.surveying')
}

/** The first three names, then how many more. */
function shortList(names: string[]) {
  const rest = names.length > 3 ? t('researchSteps.andMore', { count: names.length - 3 }) : ''
  return names.slice(0, 3).join(', ') + rest
}

function doneLabel(event: StoredResearchStep, fallback?: string) {
  const count = event.count ?? 0
  const query = event.query
  switch (event.kind) {
    case 'search': {
      const found = t('researchSteps.found', { count })
      return query ? t('researchSteps.queryOutcome', { query, outcome: found }) : found
    }
    case 'read': {
      const titles = event.titles ?? []
      if (titles.length > 0) {
        return t(event.distilled ? 'researchSteps.summarisedTitles' : 'researchSteps.readTitles', {
          titles: shortList(titles),
        })
      }
      return (
        fallback ?? t(event.distilled ? 'researchSteps.summarisedDocuments' : 'researchSteps.readDocuments')
      )
    }
    case 'survey':
      return query
        ? t('researchSteps.surveyedQuery', { count, query })
        : t('researchSteps.surveyed', { count })
    case 'count':
      return query ? t('researchSteps.countedQuery', { count, query }) : t('researchSteps.counted', { count })
    case 'web_search':
      return query
        ? t('researchSteps.queryOutcome', { query, outcome: t('researchSteps.results', { count }) })
        : t('researchSteps.searchedWeb', { count })
    case 'web_fetch': {
      // Titles are hostnames here: a URL is too long for a step line, and the
      // page's own title is not known until it has been read.
      const hosts = event.titles ?? []
      return hosts.length > 0
        ? t('researchSteps.readTitles', { titles: shortList(hosts) })
        : (fallback ?? t('researchSteps.readWebPages'))
    }
    default:
      return t('researchSteps.answerWritten')
  }
}
