import { t } from '../i18n'
import { CORRESPONDENT_FIELD_ID, DOCUMENT_TYPE_FIELD_ID, type CustomField } from './api/customFields'

/** Mirrors the constants in backend/internal/rules/rules.go. */
export type RuleTrigger = 'processed' | 'edited'

export type RuleOp =
  | 'is'
  | 'is_not'
  | 'contains'
  | 'not_contains'
  | 'matches'
  | 'gt'
  | 'lt'
  | 'is_set'
  | 'is_not_set'

export type RuleCondition = {
  /** A field id, or one of the subjects below. */
  subject: string
  op: RuleOp
  value?: string | number
}

export type RuleActionType = 'add_tag' | 'remove_tag' | 'set_field' | 'require_review' | 'ai_extract' | 'webhook'

/** Every type's parameters in one shape, as the server stores them; each type reads its own. */
export type RuleAction = {
  type: RuleActionType
  value?: string | number
  field?: string
  prompt?: string
  fields?: string[]
  url?: string
  secret?: string
}

export const RULE_TRIGGERS: RuleTrigger[] = ['processed', 'edited']

export const ACTION_TYPES: RuleActionType[] = [
  'add_tag',
  'remove_tag',
  'set_field',
  'require_review',
  'ai_extract',
  'webhook',
]

export const SUBJECT_TITLE = 'title'
export const SUBJECT_TEXT = 'text'
export const SUBJECT_TAG = 'tag'
export const SUBJECT_CONFIDENCE = 'confidence'
/** A yes/no question the AI answers about the document; its value is the question. */
export const SUBJECT_AI = 'ai'

export type SubjectKind = 'text' | 'number' | 'date' | 'option' | 'choice' | 'tag' | 'ai'

/** Mirrors opsByKind in backend/internal/rules/rules.go. */
export const OPS_BY_KIND: Record<SubjectKind, RuleOp[]> = {
  text: ['contains', 'not_contains', 'matches', 'is_set', 'is_not_set'],
  number: ['is', 'gt', 'lt', 'is_set', 'is_not_set'],
  date: ['is', 'gt', 'lt', 'is_set', 'is_not_set'],
  option: ['is', 'is_not', 'is_set', 'is_not_set'],
  choice: ['is', 'is_not', 'is_set', 'is_not_set'],
  tag: ['is', 'is_not'],
  ai: ['is', 'is_not'],
}

/** What a condition's subject is, or null for a field deleted since. */
export function subjectKind(subject: string, fields: CustomField[]): SubjectKind | null {
  switch (subject) {
    case SUBJECT_TITLE:
    case SUBJECT_TEXT:
      return 'text'
    case SUBJECT_TAG:
      return 'tag'
    case SUBJECT_CONFIDENCE:
      return 'number'
    case SUBJECT_AI:
      return 'ai'
    case DOCUMENT_TYPE_FIELD_ID:
    case CORRESPONDENT_FIELD_ID:
      return 'option'
  }
  const field = fields.find((f) => f.id === subject)
  if (!field) return null
  return field.type
}

export function needsValue(op: RuleOp): boolean {
  return op !== 'is_set' && op !== 'is_not_set'
}

/** A fresh condition on subject: its kind's first comparison and an empty value. */
export function conditionFor(subject: string, fields: CustomField[]): RuleCondition {
  const kind = subjectKind(subject, fields) ?? 'text'
  return { subject, op: OPS_BY_KIND[kind][0], value: '' }
}

/** Keeps the comparison when the new kind has it, so switching between two text fields loses nothing. */
export function withSubject(condition: RuleCondition, subject: string, fields: CustomField[]): RuleCondition {
  const next = conditionFor(subject, fields)
  const before = subjectKind(condition.subject, fields)
  const after = subjectKind(subject, fields)
  if (before === after && after !== 'option' && after !== 'choice') {
    return { ...condition, subject }
  }
  return next
}

export function defaultAction(type: RuleActionType): RuleAction {
  switch (type) {
    case 'set_field':
      return { type, field: DOCUMENT_TYPE_FIELD_ID, value: '' }
    case 'ai_extract':
      return { type, prompt: '', fields: [] }
    case 'webhook':
      return { type, url: '', secret: '' }
    case 'require_review':
      return { type }
    default:
      return { type, value: '' }
  }
}

/**
 * What a rule is saved as: a value only where its comparison takes one, and a
 * number field's value as a number. An input holds what was typed.
 */
export function ruleConditionsForSave(conditions: RuleCondition[], fields: CustomField[]): RuleCondition[] {
  return conditions.map((c) => {
    if (!needsValue(c.op)) return { subject: c.subject, op: c.op }
    return { ...c, value: valueForSave(subjectKind(c.subject, fields), c.value) }
  })
}

export function ruleActionsForSave(actions: RuleAction[], fields: CustomField[]): RuleAction[] {
  return actions.map((a) => {
    switch (a.type) {
      case 'set_field':
        return { type: a.type, field: a.field, value: valueForSave(subjectKind(a.field ?? '', fields), a.value) }
      case 'ai_extract':
        return { type: a.type, prompt: (a.prompt ?? '').trim(), fields: a.fields ?? [] }
      case 'webhook':
        return { type: a.type, url: (a.url ?? '').trim(), ...(a.secret ? { secret: a.secret } : {}) }
      case 'require_review':
        return { type: a.type }
      default:
        return { type: a.type, value: a.value }
    }
  })
}

function valueForSave(kind: SubjectKind | null, value: RuleCondition['value']): RuleCondition['value'] {
  if (kind !== 'number') return typeof value === 'string' ? value.trim() : value
  const n = typeof value === 'number' ? value : Number(String(value ?? '').trim().replace(',', '.'))
  return String(value ?? '').trim() !== '' && Number.isFinite(n) ? n : value
}

export const TRIGGER_LABELS: Record<RuleTrigger, string> = {
  processed: t('rules.triggerProcessed'),
  edited: t('rules.triggerEdited'),
}

export const ACTION_LABELS: Record<RuleActionType, string> = {
  add_tag: t('rules.actionAddTag'),
  remove_tag: t('rules.actionRemoveTag'),
  set_field: t('rules.actionSetField'),
  require_review: t('rules.actionRequireReview'),
  ai_extract: t('rules.actionAIExtract'),
  webhook: t('rules.actionWebhook'),
}

/** A comparison's words depend on what it compares: a tag is had, a date is after. */
export function opLabel(op: RuleOp, kind: SubjectKind): string {
  if (kind === 'tag') return op === 'is' ? t('rules.opHas') : t('rules.opHasNot')
  if (kind === 'ai') return op === 'is' ? t('rules.opAnswersYes') : t('rules.opAnswersNo')
  if (kind === 'date' && op === 'gt') return t('rules.opAfter')
  if (kind === 'date' && op === 'lt') return t('rules.opBefore')
  return {
    is: t('rules.opIs'),
    is_not: t('rules.opIsNot'),
    contains: t('rules.opContains'),
    not_contains: t('rules.opNotContains'),
    matches: t('rules.opMatches'),
    gt: t('rules.opGreater'),
    lt: t('rules.opLess'),
    is_set: t('rules.opIsSet'),
    is_not_set: t('rules.opIsNotSet'),
  }[op]
}
