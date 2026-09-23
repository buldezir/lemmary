import { describe, expect, test } from 'vitest'
import { pendingTagSuggestions, tagKey } from './tagSuggestions'

describe('tag suggestions', () => {
  test('tagKey folds case, accents and punctuation like the worker does', () => {
    expect(tagKey('  Büro-Kosten ')).toBe('burokosten')
    expect(tagKey('---')).toBe('')
  })

  test('drops names already on the document, duplicates and blanks', () => {
    const job = { metadata_json: { suggested_tags: ['Warranty', ' warranty', 'invoices', '  ', 'Heating'] } }
    expect(pendingTagSuggestions(job, ['Invoices'])).toEqual(['Warranty', 'Heating'])
  })

  test('is empty without a job or suggestions', () => {
    expect(pendingTagSuggestions(null, [])).toEqual([])
    expect(pendingTagSuggestions({ metadata_json: {} }, [])).toEqual([])
  })
})
