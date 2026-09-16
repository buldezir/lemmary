import { describe, expect, it } from 'vitest'
import { ClientResponseError } from 'pocketbase'
import { duplicateNameError } from './tags'

function notUnique(field: string) {
  return new ClientResponseError({
    status: 400,
    response: {
      data: { [field]: { code: 'validation_not_unique', message: 'Value must be unique.' } },
    },
  })
}

describe('duplicateNameError', () => {
  // PocketBase's own message is "Value must be unique.", which names neither
  // the field nor the value.
  it('names the tag behind a unique-index violation', () => {
    expect(duplicateNameError(notUnique('name'), 'Invoices').message).toBe(
      'You already have a tag called "Invoices".',
    )
  })

  // A 400 on some other field is a different problem and must keep its own
  // message rather than being reported as a duplicate name.
  it('leaves a validation error on another field alone', () => {
    const err = notUnique('user')
    expect(duplicateNameError(err, 'Invoices')).toBe(err)
  })

  it('passes any other error through', () => {
    const err = new Error('network down')
    expect(duplicateNameError(err, 'Invoices')).toBe(err)
  })

  it('still yields an Error for a non-Error rejection', () => {
    expect(duplicateNameError('nope', 'Invoices')).toBeInstanceOf(Error)
  })
})
