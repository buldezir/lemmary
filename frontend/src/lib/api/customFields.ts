import { ClientResponseError } from 'pocketbase'
import { t } from '../../i18n'
import { ensureAuth } from '../auth'
import { pb } from '../pb'

/** The predefined option fields, whose ids are fixed. */
export const CORRESPONDENT_FIELD_ID = 'fcorrespondent0'
export const DOCUMENT_TYPE_FIELD_ID = 'fdocumenttype00'

export type CustomFieldType = 'text' | 'number' | 'date' | 'choice'

export const CUSTOM_FIELD_TYPES: CustomFieldType[] = ['text', 'number', 'date', 'choice']

/** One of a choice field's fixed values; a new one has no id yet. */
export type CustomFieldChoice = { id: string; name: string }

/** An admin-defined document field: one record of the custom_fields collection. */
export type CustomField = {
  id: string
  name: string
  type: CustomFieldType
  /** A hint for extraction: what the field means, where it appears. */
  description: string
  /** A choice field's values, in the order they were added; empty otherwise. */
  choices: CustomFieldChoice[]
}

export type CustomFieldInput = Omit<CustomField, 'id'>

/** What PUT /fields takes, by field id: null clears, an option field takes a name. */
export type CustomFieldValues = Record<string, string | number | null>

/** One custom_field_values row, with the option expanded for an option field. */
export type FieldValueRecord = {
  id: string
  field: string
  text: string
  number: number
  date: string
  option: string
  choice: string
  expand?: { option?: { id: string; name: string }; choice?: CustomFieldChoice }
}

/** The expand that brings a document's values along with it. */
export const FIELD_VALUES_EXPAND =
  'custom_field_values_via_document.option,custom_field_values_via_document.choice'

type WithFieldValues = { expand?: { custom_field_values_via_document?: FieldValueRecord[] } }

function fieldValueRow(document: WithFieldValues, fieldId: string) {
  return document.expand?.custom_field_values_via_document?.find((value) => value.field === fieldId)
}

/** The name of the option an option field points at, or "". */
export function optionName(document: WithFieldValues, fieldId: string): string {
  return fieldValueRow(document, fieldId)?.expand?.option?.name ?? ''
}

/** A field's value as its input holds it, "" when the document has none. */
export function fieldInputValue(document: WithFieldValues, field: CustomField): string {
  const value = fieldValueRow(document, field.id)
  if (!value) return ''
  if (field.type === 'number') return String(value.number)
  if (field.type === 'date') return value.date.slice(0, 10)
  if (field.type === 'choice') return value.expand?.choice?.name ?? ''
  return value.text
}

/** The admin's fields; the predefined option fields have inputs of their own. */
export async function listCustomFields(): Promise<CustomField[]> {
  await ensureAuth()
  const [fields, choices] = await Promise.all([
    pb
      .collection('custom_fields')
      .getFullList<Omit<CustomField, 'choices'>>({ filter: "type != 'option'", sort: 'created,id' }),
    pb
      .collection('custom_field_choices')
      .getFullList<CustomFieldChoice & { field: string }>({ sort: 'created,id' }),
  ])
  return fields.map((field) => ({
    ...field,
    choices: choices.filter((c) => c.field === field.id).map(({ id, name }) => ({ id, name })),
  }))
}

/**
 * Creates the field when id is empty, otherwise updates it, then brings a
 * choice field's choices from saved to input's. Admins only.
 */
export async function saveCustomField(
  id: string,
  input: CustomFieldInput,
  saved: CustomFieldChoice[] = [],
): Promise<void> {
  await ensureAuth()
  const body = {
    name: input.name.trim(),
    type: input.type,
    description: input.description.trim(),
  }
  const choices = input.type === 'choice' ? choiceChanges(saved, input.choices) : null
  const collection = pb.collection('custom_fields')
  const fieldId = await (id
    ? collection.update<CustomField>(id, body)
    : collection.create<CustomField>(body)
  )
    .then((field) => field.id)
    .catch((err: unknown) => {
      throw customFieldSaveError(err, body.name)
    })
  if (!choices) return
  const records = pb.collection('custom_field_choices')
  for (const choiceId of choices.remove) await records.delete(choiceId)
  for (const choice of choices.rename) await records.update(choice.id, { name: choice.name })
  for (const name of choices.add) await records.create({ field: fieldId, name })
}

/**
 * What turns the saved choices into the edited ones, removals first so a name
 * can move to a new choice. Refuses two names that differ only in case before
 * anything is sent, as the unique index would halfway through. Exported for
 * its test.
 */
export function choiceChanges(saved: CustomFieldChoice[], edited: CustomFieldChoice[]) {
  const seen = new Set<string>()
  for (const choice of edited) {
    const key = choice.name.trim().toLowerCase()
    if (seen.has(key)) {
      throw new Error(t('customFieldsApi.duplicateChoice', { name: choice.name.trim() }))
    }
    seen.add(key)
  }
  const kept = new Map(edited.filter((c) => c.id).map((c) => [c.id, c.name.trim()]))
  return {
    remove: saved.filter((c) => !kept.has(c.id)).map((c) => c.id),
    rename: saved
      .filter((c) => kept.has(c.id) && kept.get(c.id) !== c.name)
      .map((c) => ({ id: c.id, name: kept.get(c.id)! })),
    add: edited.filter((c) => !c.id).map((c) => c.name.trim()),
  }
}

export async function deleteCustomField(id: string): Promise<void> {
  await ensureAuth()
  await pb.collection('custom_fields').delete(id)
}

/**
 * The collection's unique index ignores case, and PocketBase reports a clash
 * as a per-field code whose message never names the value. Exported for its test.
 */
export function customFieldSaveError(err: unknown, name: string): Error {
  if (err instanceof ClientResponseError) {
    const field = (err.response?.data as Record<string, { code?: string }> | undefined)?.name
    if (field?.code === 'validation_not_unique') {
      return new Error(t('customFieldsApi.duplicateName', { name }))
    }
  }
  return err instanceof Error ? err : new Error(t('customFieldsApi.saveFailed'))
}

/**
 * What a document save sends for the fields edited on the page: a blank
 * clears, a number field's input goes as a number. A field nobody touched is
 * not sent, so the save leaves it as it is.
 */
export function customFieldValuesForSave(
  fields: CustomField[],
  inputs: Record<string, string>,
): CustomFieldValues {
  const out: CustomFieldValues = {}
  for (const field of fields) {
    if (!(field.id in inputs)) continue
    const value = inputs[field.id].trim()
    const n = field.type === 'number' && value !== '' ? Number(value) : NaN
    out[field.id] = value === '' ? null : Number.isFinite(n) ? n : value
  }
  return out
}
