import { describe, expect, it } from 'vitest'
import { ClientResponseError } from 'pocketbase'
import {
  CORRESPONDENT_FIELD_ID,
  customFieldSaveError,
  customFieldValuesForSave,
  fieldInputValue,
  optionName,
  type CustomField,
} from './customFields'

const fields: CustomField[] = [
  { id: 'finvoice', name: 'Invoice number', type: 'text', description: '', choices: [] },
  { id: 'famount', name: 'Amount', type: 'number', description: '', choices: [] },
  { id: 'fdue', name: 'Due date', type: 'date', description: '', choices: [] },
  {
    id: 'fstatus',
    name: 'Status',
    type: 'choice',
    description: '',
    choices: [{ id: 'c1', name: 'Paid' }],
  },
]

describe('customFieldValuesForSave', () => {
  it('trims text, turns number input into numbers and clears blanks', () => {
    expect(
      customFieldValuesForSave(fields, {
        finvoice: ' R-1 ',
        famount: '129.90',
        fdue: '',
      }),
    ).toEqual({ finvoice: 'R-1', famount: 129.9, fdue: null })
  })

  // Only what was edited goes out, so a save never touches a field nobody changed.
  it('leaves out the fields that were not edited', () => {
    expect(customFieldValuesForSave(fields, { famount: '42' })).toEqual({ famount: 42 })
  })

  it('ignores inputs for fields it has no definition for', () => {
    expect(customFieldValuesForSave([], { finvoice: 'R-1' })).toEqual({})
  })
})

describe('fieldInputValue and optionName', () => {
  const document = {
    expand: {
      custom_field_values_via_document: [
        { id: 'v1', field: 'famount', text: '', number: 12.5, date: '', option: '', choice: '' },
        {
          id: 'v2',
          field: 'fdue',
          text: '',
          number: 0,
          date: '2026-10-31 00:00:00.000Z',
          option: '',
          choice: '',
        },
        {
          id: 'v4',
          field: 'fstatus',
          text: '',
          number: 0,
          date: '',
          option: '',
          choice: 'c1',
          expand: { choice: { id: 'c1', name: 'Paid' } },
        },
        {
          id: 'v3',
          field: CORRESPONDENT_FIELD_ID,
          text: '',
          number: 0,
          date: '',
          option: 'o1',
          choice: '',
          expand: { option: { id: 'o1', name: 'Acme' } },
        },
      ],
    },
  }

  it('reads each value in the shape its input takes', () => {
    expect(fieldInputValue(document, fields[1])).toBe('12.5')
    expect(fieldInputValue(document, fields[2])).toBe('2026-10-31')
    expect(fieldInputValue(document, fields[0])).toBe('')
    expect(fieldInputValue(document, fields[3])).toBe('Paid')
    expect(optionName(document, CORRESPONDENT_FIELD_ID)).toBe('Acme')
    expect(optionName({}, CORRESPONDENT_FIELD_ID)).toBe('')
  })
})

describe('customFieldSaveError', () => {
  function notUnique(field: string) {
    return new ClientResponseError({
      status: 400,
      response: {
        data: {
          [field]: {
            code: 'validation_not_unique',
            message: 'Value must be unique.',
          },
        },
      },
    })
  }

  it('names the field behind a unique-index violation', () => {
    expect(customFieldSaveError(notUnique('name'), 'Amount').message).toBe(
      'A custom field called "Amount" already exists.',
    )
  })

  it('passes any other error through', () => {
    const err = notUnique('type')
    expect(customFieldSaveError(err, 'Amount')).toBe(err)
  })
})
