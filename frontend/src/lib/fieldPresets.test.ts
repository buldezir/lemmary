import { describe, expect, it } from 'vitest'
import { languageNames, translator, type Language } from '../i18n'
import { fieldPresets, missingFields } from './fieldPresets'

describe('missingFields', () => {
  it('skips a preset field already defined under the same name, ignoring case and spaces', () => {
    const [invoices] = fieldPresets()
    const defined = [
      { id: 'f1', name: ' invoice NUMBER ', type: 'number' as const, description: '', choices: [] },
    ]
    expect(missingFields(invoices, defined).map((f) => f.name)).toEqual([
      'Amount',
      'Currency',
      'Due date',
      'Payment status',
    ])
  })

  it('returns nothing once every field is defined', () => {
    const [invoices] = fieldPresets()
    const defined = invoices.fields.map((f, i) => ({ ...f, id: `f${i}` }))
    expect(missingFields(invoices, defined)).toEqual([])
  })
})

describe('fieldPresets', () => {
  it.each(Object.keys(languageNames) as Language[])('fits the field limits in %s', (language) => {
    const { t } = translator(language)
    const byName = new Map<string, string>()
    for (const preset of fieldPresets(t)) {
      const names = preset.fields.map((f) => f.name.toLowerCase())
      expect(new Set(names).size, preset.title).toBe(names.length)
      for (const field of preset.fields) {
        expect(field.name.length).toBeLessThanOrEqual(100)
        expect(field.description.length).toBeLessThanOrEqual(500)
        expect(field.choices.length > 0, field.name).toBe(field.type === 'choice')
        const definition = JSON.stringify(field)
        const key = field.name.toLowerCase()
        expect(byName.get(key) ?? definition, field.name).toBe(definition)
        byName.set(key, definition)
      }
    }
  })
})
