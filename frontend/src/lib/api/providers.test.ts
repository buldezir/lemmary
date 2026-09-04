import { describe, expect, it } from 'vitest'

import {
  canEmbedProvider,
  isLLMProvider,
  providerServesPurpose,
  requiresAPIKey,
  SDK_DEFAULT_BASE,
  SDK_OPTIONS,
} from './providers'

// These four mirror Go predicates in internal/aiprovider/sdk.go. They are
// duplicated rather than fetched because the pickers have to decide before any
// request is made -- so the risk is the two drifting apart, and that is what
// these assert.
describe('SDK capabilities', () => {
  it('treats local as an embedding-only SDK', () => {
    expect(canEmbedProvider('local')).toBe(true)
    expect(isLLMProvider('local')).toBe(false)
  })

  it('keeps google_vision out of the embedding pickers', () => {
    // It reads documents; it has no /embeddings endpoint at all.
    expect(canEmbedProvider('google_vision')).toBe(false)
    expect(isLLMProvider('google_vision')).toBe(false)
  })

  it('lets the hosted SDKs do both', () => {
    for (const sdk of ['openai', 'openrouter', 'mistral']) {
      expect(isLLMProvider(sdk)).toBe(true)
      expect(canEmbedProvider(sdk)).toBe(true)
    }
  })

  it('asks for a key everywhere but the two sidecars', () => {
    expect(requiresAPIKey('local')).toBe(false)
    expect(requiresAPIKey('docling')).toBe(false)
    for (const sdk of ['openai', 'openrouter', 'mistral', 'google_vision']) {
      expect(requiresAPIKey(sdk)).toBe(true)
    }
    // Default-true like the Go side, so an unknown SDK still asks.
    expect(requiresAPIKey(undefined)).toBe(true)
  })
})

describe('providerServesPurpose', () => {
  it('offers a local provider to the embedding picker only', () => {
    expect(providerServesPurpose('local', 'embedding')).toBe(true)
    expect(providerServesPurpose('local', 'llm')).toBe(false)
    expect(providerServesPurpose('local', 'ocr')).toBe(false)
  })

  it('offers the docling sidecar to OCR only', () => {
    expect(providerServesPurpose('docling', 'ocr')).toBe(true)
    expect(providerServesPurpose('docling', 'llm')).toBe(false)
    expect(providerServesPurpose('docling', 'embedding')).toBe(false)
  })

  it('offers google_vision to OCR only', () => {
    expect(providerServesPurpose('google_vision', 'ocr')).toBe(true)
    expect(providerServesPurpose('google_vision', 'llm')).toBe(false)
    expect(providerServesPurpose('google_vision', 'embedding')).toBe(false)
  })

  it('offers a hosted provider to every picker', () => {
    for (const purpose of ['ocr', 'llm', 'embedding'] as const) {
      expect(providerServesPurpose('mistral', purpose)).toBe(true)
    }
  })
})

describe('the local SDK is offered and addressed', () => {
  it('appears in the Add provider dropdown', () => {
    expect(SDK_OPTIONS.map((option) => option.value)).toContain('local')
  })

  it('defaults to the compose service name', () => {
    // Keep in step with aiprovider.DefaultBaseURL(SDKLocal) and the service
    // name in docker-compose.embeddings.yml, or the overlay comes up
    // unconfigured.
    expect(SDK_DEFAULT_BASE.local).toBe('http://embeddings:80/v1')
  })
})
