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
 * Creates the field when id is empty, otherwise updates it. A choice field's
 * choices travel with it, in order, each new one without an id: the server
 * stores the field and the whole list in one transaction. Admins only.
 */
export async function saveCustomField(id: string, input: CustomFieldInput): Promise<void> {
  await ensureAuth()
  const body = {
    name: input.name.trim(),
    type: input.type,
    description: input.description.trim(),
    ...(input.type === 'choice' && {
      choices: input.choices.map((c) => ({ id: c.id, name: c.name.trim() })),
    }),
  }
  const collection = pb.collection('custom_fields')
  try {
    await (id ? collection.update(id, body) : collection.create(body))
  } catch (err) {
    throw customFieldSaveError(err, body.name)
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
