import { describe, expect, it } from 'vitest'

import { asPickerProvider, bindingBody, bindingIsEmpty } from './providers'
import { describeJobOverrides, jobOverridesBody } from './documents'
import { chatSessionBinding, type ChatSession } from './chats'

describe('bindingBody', () => {
  // The whole point of the "or nothing" shape: a page whose picker is never
  // opened has to send exactly what it sent before overrides existed.
  it('sends nothing when no provider was chosen', () => {
    expect(bindingBody(undefined)).toEqual({})
    expect(bindingBody({ provider_id: '', model: '' })).toEqual({})
    expect(bindingBody({ provider_id: '   ', model: 'gpt-6-astra' })).toEqual({})
  })

  it('sends both halves, trimmed', () => {
    expect(bindingBody({ provider_id: ' p1 ', model: ' gpt-6-astra ' })).toEqual({
      provider_id: 'p1',
      model: 'gpt-6-astra',
    })
  })

  // A provider with no model is legitimate: the server falls back to the
  // configured model for that provider rather than refusing the request.
  it('sends a provider with no model', () => {
    expect(bindingBody({ provider_id: 'p1', model: '' })).toEqual({
      provider_id: 'p1',
      model: '',
    })
  })
})

describe('bindingIsEmpty', () => {
  it('keys on the provider, matching aiprovider.Binding.Empty', () => {
    expect(bindingIsEmpty(undefined)).toBe(true)
    expect(bindingIsEmpty({ provider_id: '', model: 'gpt-6-astra' })).toBe(true)
    expect(bindingIsEmpty({ provider_id: 'p1', model: '' })).toBe(false)
  })
})

describe('jobOverridesBody', () => {
  it('sends nothing when no binding was chosen', () => {
    expect(jobOverridesBody(undefined)).toEqual({})
    expect(jobOverridesBody({})).toEqual({})
    // An opened-but-unfilled picker is not a choice yet.
    expect(jobOverridesBody({ extract: { provider_id: '', model: '' } })).toEqual({})
  })

  it('sends only the bindings that were chosen', () => {
    expect(
      jobOverridesBody({
        ocr: { provider_id: 'p1', model: 'mistral-ocr-latest' },
        extract: { provider_id: '', model: '' },
      }),
    ).toEqual({ overrides: { ocr: { provider_id: 'p1', model: 'mistral-ocr-latest' } } })
  })
})

describe('describeJobOverrides', () => {
  it('is empty when nothing was overridden', () => {
    expect(describeJobOverrides(undefined)).toBe('')
    expect(describeJobOverrides({ extract: { provider_id: '', model: 'x' } })).toBe('')
  })

  it('names each overridden binding and its model', () => {
    expect(
      describeJobOverrides({
        extract: { provider_id: 'p1', model: 'gpt-6-astra' },
        ocr: { provider_id: 'p2', model: '' },
      }),
    ).toBe('extract: gpt-6-astra, ocr: provider default')
  })
})

describe('chatSessionBinding', () => {
  const base: ChatSession = {
    id: 's1',
    kind: 'search',
    title: 'A chat',
    message_count: 2,
    last_message_at: '2026-09-08 10:00:00.000Z',
    created: '2026-09-08 10:00:00.000Z',
    updated: '2026-09-08 10:00:00.000Z',
  }

  it('is undefined for a chat on the configured model', () => {
    expect(chatSessionBinding(null)).toBeUndefined()
    expect(chatSessionBinding(base)).toBeUndefined()
  })

  it('restores the pinned binding', () => {
    expect(chatSessionBinding({ ...base, provider: 'p1', model: 'gpt-6-astra' })).toEqual({
      provider_id: 'p1',
      model: 'gpt-6-astra',
    })
  })

  // The server refuses a model with no provider, so offering it back as a
  // choice would only produce a request it will not accept.
  it('ignores a model with no provider', () => {
    expect(chatSessionBinding({ ...base, model: 'gpt-6-astra' })).toBeUndefined()
  })
})

describe('asPickerProvider', () => {
  // ProviderModelFields was written for Settings, where a provider is the full
  // record. It reads only id, sdk and alias; the credential flags are true
  // because /api/app/ai/providers only returns configured providers.
  it('maps a pickable row onto the shape the picker wants', () => {
    expect(asPickerProvider({ id: 'p1', name: 'My OpenAI', sdk: 'openai' })).toEqual({
      id: 'p1',
      sdk: 'openai',
      alias: 'My OpenAI',
      base_url: '',
      api_key_set: true,
      signed_in: true,
    })
  })
})
