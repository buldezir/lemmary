import { describe, expect, test } from 'vitest'

import { CORRESPONDENT_FIELD_ID, DOCUMENT_TYPE_FIELD_ID, type CustomField } from './api/customFields'
import {
  OPS_BY_KIND,
  conditionFor,
  opLabel,
  ruleActionsForSave,
  ruleConditionsForSave,
  subjectKind,
  withSubject,
} from './rules'

const fields: CustomField[] = [
  { id: 'famount', name: 'Amount', type: 'number', description: '', choices: [] },
  { id: 'fnumber', name: 'Invoice number', type: 'text', description: '', choices: [] },
  { id: 'fstatus', name: 'Status', type: 'choice', description: '', choices: [{ id: 'cpaid', name: 'Paid' }] },
]

describe('subjectKind', () => {
  test('reads the predefined subjects and the admin fields', () => {
    expect(subjectKind('title', fields)).toBe('text')
    expect(subjectKind('tag', fields)).toBe('tag')
    expect(subjectKind('confidence', fields)).toBe('number')
    expect(subjectKind(DOCUMENT_TYPE_FIELD_ID, fields)).toBe('option')
    expect(subjectKind(CORRESPONDENT_FIELD_ID, fields)).toBe('option')
    expect(subjectKind('famount', fields)).toBe('number')
    expect(subjectKind('fstatus', fields)).toBe('choice')
    expect(subjectKind('ai', fields)).toBe('ai')
  })

  test('is null for a field deleted since', () => {
    expect(subjectKind('fgone', fields)).toBeNull()
  })
})

describe('changing a condition subject', () => {
  test('starts from the new kind first comparison', () => {
    expect(conditionFor('famount', fields)).toEqual({ subject: 'famount', op: 'is', value: '' })
    expect(withSubject({ subject: 'title', op: 'matches', value: 'x' }, 'famount', fields)).toEqual({
      subject: 'famount',
      op: 'is',
      value: '',
    })
  })

  test('keeps what was typed between two text subjects', () => {
    expect(withSubject({ subject: 'title', op: 'matches', value: 'R-\\d+' }, 'fnumber', fields)).toEqual({
      subject: 'fnumber',
      op: 'matches',
      value: 'R-\\d+',
    })
  })

  test('drops an id that belongs to another list', () => {
    expect(
      withSubject({ subject: DOCUMENT_TYPE_FIELD_ID, op: 'is', value: 'oinvoice' }, CORRESPONDENT_FIELD_ID, fields),
    ).toEqual({ subject: CORRESPONDENT_FIELD_ID, op: 'is', value: '' })
  })
})

describe('saving a rule', () => {
  test('sends a number field value as a number and no value where none is read', () => {
    expect(
      ruleConditionsForSave(
        [
          { subject: 'famount', op: 'gt', value: '1000,5' },
          { subject: 'fnumber', op: 'is_set', value: 'left over' },
          { subject: 'title', op: 'contains', value: '  invoice ' },
        ],
        fields,
      ),
    ).toEqual([
      { subject: 'famount', op: 'gt', value: 1000.5 },
      { subject: 'fnumber', op: 'is_set' },
      { subject: 'title', op: 'contains', value: 'invoice' },
    ])
  })

  test('keeps only each action type own parameters', () => {
    expect(
      ruleActionsForSave(
        [
          { type: 'set_field', field: 'famount', value: '12', prompt: 'stale' },
          { type: 'webhook', url: ' https://x.test/h ', secret: '' },
          { type: 'ai_extract', prompt: ' gross ', fields: ['famount'], url: 'stale' },
          { type: 'require_review', value: 'stale' },
        ],
        fields,
      ),
    ).toEqual([
      { type: 'set_field', field: 'famount', value: 12 },
      { type: 'webhook', url: 'https://x.test/h' },
      { type: 'ai_extract', prompt: 'gross', fields: ['famount'] },
      { type: 'require_review' },
    ])
  })
})

test('every comparison has words for its kind', () => {
  for (const [kind, ops] of Object.entries(OPS_BY_KIND)) {
    for (const op of ops) {
      expect(opLabel(op, kind as keyof typeof OPS_BY_KIND)).toBeTruthy()
    }
  }
  expect(opLabel('gt', 'date')).not.toBe(opLabel('gt', 'number'))
})
