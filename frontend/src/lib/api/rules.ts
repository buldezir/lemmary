import { ClientResponseError } from 'pocketbase'
import { t } from '../../i18n'
import { ensureAuth } from '../auth'
import { pb } from '../pb'
import type { RuleAction, RuleCondition, RuleTrigger } from '../rules'

/** One record of the rules collection. Mirrors backend/internal/rules. */
export type RuleRecord = {
  id: string
  user: string
  name: string
  enabled: boolean
  position: number
  triggers: RuleTrigger[]
  conditions: RuleCondition[] | null
  actions: RuleAction[] | null
}

export type RuleInput = Pick<RuleRecord, 'name' | 'enabled' | 'triggers'> & {
  conditions: RuleCondition[]
  actions: RuleAction[]
}

/**
 * The owner's rules in the order they run, straight through the SDK: the
 * collection's rules scope every row to its owner, and the server checks each
 * rule's conditions and actions on save.
 */
export async function listRules(): Promise<RuleRecord[]> {
  await ensureAuth()
  return pb.collection('rules').getFullList<RuleRecord>({ sort: 'position,created' })
}

/** Creates the rule last in the order when id is empty, otherwise updates it. */
export async function saveRule(id: string, input: RuleInput, position: number): Promise<void> {
  await ensureAuth()
  const body = { ...input, name: input.name.trim() }
  try {
    if (id) {
      await pb.collection('rules').update(id, body)
    } else {
      await pb.collection('rules').create({ ...body, position, user: pb.authStore.record?.id })
    }
  } catch (err) {
    throw ruleSaveError(err)
  }
}

export async function setRuleEnabled(id: string, enabled: boolean): Promise<void> {
  await ensureAuth()
  await pb.collection('rules').update(id, { enabled })
}

/** Stores the order as given, writing only the rules whose place changed. */
export async function reorderRules(ordered: RuleRecord[]): Promise<void> {
  await ensureAuth()
  for (const [position, rule] of ordered.entries()) {
    if (rule.position !== position) {
      await pb.collection('rules').update(rule.id, { position })
    }
  }
}

export async function deleteRule(id: string): Promise<void> {
  await ensureAuth()
  await pb.collection('rules').delete(id)
}

/** The server's own words for a refused rule; they are already translated. */
export function ruleSaveError(err: unknown): Error {
  if (err instanceof ClientResponseError && err.status === 400 && err.response?.message) {
    return new Error(String(err.response.message))
  }
  return err instanceof Error ? err : new Error(t('rulesApi.saveFailed'))
}
