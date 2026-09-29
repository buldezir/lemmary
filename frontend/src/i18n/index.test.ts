import { isValidElement, type ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { de } from './de'
import { en } from './en'
import { lang, pickLanguage, translator, type Catalog, type MessageKey } from '.'
import { ru } from './ru'

function placeholders(entry: Catalog[MessageKey]): string[] {
  const text = typeof entry === 'string' ? entry : Object.values(entry).join(' ')
  return [...new Set(text.match(/\{\w+\}/g) ?? [])].sort()
}

describe('pickLanguage', () => {
  it('prefers the stored choice', () => {
    expect(pickLanguage('ru', ['de-DE', 'en'])).toBe('ru')
  })

  it('falls back to the first supported browser language by primary subtag', () => {
    expect(pickLanguage(null, ['fr-FR', 'de-AT', 'en'])).toBe('de')
    expect(pickLanguage(null, ['en-GB'])).toBe('en')
  })

  it('ignores an unknown stored value and anything off the prototype', () => {
    expect(pickLanguage('constructor', ['xx', 'toString'])).toBe('en')
  })

  it('is English under Node, so unit tests see English labels', () => {
    expect(lang).toBe('en')
  })
})

describe('translator', () => {
  it('fills placeholders and leaves unknown ones visible', () => {
    const { t } = translator('en')
    expect(t('common.documents', { count: 1 })).toBe('1 document')
    expect(t('common.documents', { count: 4 })).toBe('4 documents')
    expect(t('common.untitledDocument', { unused: 'x' })).toBe('Untitled document')
  })

  it('picks the Russian plural form', () => {
    const { t } = translator('ru')
    expect(t('common.documents', { count: 1 })).toBe('1 документ')
    expect(t('common.documents', { count: 2 })).toBe('2 документа')
    expect(t('common.documents', { count: 5 })).toBe('5 документов')
    expect(t('common.documents', { count: 21 })).toBe('21 документ')
  })

  it('puts elements where the sentence says', () => {
    const { tNode } = translator('en')
    const node = tNode('common.documents', { count: 3 }) as ReactElement<{ children: unknown[] }>
    expect(isValidElement(node)).toBe(true)
    expect(node.props.children).toEqual(['', 3, ' documents'])
  })
})

describe('catalogs', () => {
  it.each([
    ['de', de],
    ['ru', ru],
  ])('%s has exactly the English keys and placeholders', (_, catalog) => {
    expect(Object.keys(catalog).sort()).toEqual(Object.keys(en).sort())
    for (const key of Object.keys(en) as MessageKey[]) {
      expect([key, placeholders(catalog[key])]).toEqual([key, placeholders(en[key])])
    }
  })
})
