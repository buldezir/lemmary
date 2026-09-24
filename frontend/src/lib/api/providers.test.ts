import { describe, expect, it } from 'vitest'

import {
  canEmbedProvider,
  canWebSearchProvider,
  isLLMProvider,
  providerDocs,
  keylessProviderHint,
  eligibleProviders,
  providerConfigured,
  providerOptionLabel,
  providerServesPurpose,
  recommendedModel,
  requiresAPIKey,
  requiresSignIn,
  SDK_DEFAULT_BASE,
  SDK_OPTIONS,
} from './providers'

// These four mirror Go predicates in internal/aiprovider/sdk.go, duplicated
// because the pickers decide before any request is made. Drift is the risk.
describe('SDK capabilities', () => {
  it('treats local as an embedding-only SDK', () => {
    expect(canEmbedProvider('local')).toBe(true)
    expect(isLLMProvider('local')).toBe(false)
  })

  it('keeps google_vision out of the embedding pickers', () => {
    expect(canEmbedProvider('google_vision')).toBe(false)
    expect(isLLMProvider('google_vision')).toBe(false)
  })

  it('lets the hosted SDKs do both', () => {
    for (const sdk of ['openai', 'openrouter', 'mistral']) {
      expect(isLLMProvider(sdk)).toBe(true)
      expect(canEmbedProvider(sdk)).toBe(true)
    }
    expect(isLLMProvider('opencode')).toBe(true)
    expect(canEmbedProvider('opencode')).toBe(false)
    // anthropic chats and reads documents; api.anthropic.com serves no
    // /embeddings at all.
    expect(isLLMProvider('anthropic')).toBe(true)
    expect(canEmbedProvider('anthropic')).toBe(false)
  })

  it('asks for a key everywhere but the two sidecars', () => {
    expect(requiresAPIKey('local')).toBe(false)
    expect(requiresAPIKey('docling')).toBe(false)
    for (const sdk of ['openai', 'anthropic', 'openrouter', 'mistral', 'opencode', 'google_vision']) {
      expect(requiresAPIKey(sdk)).toBe(true)
    }
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
    // Keep in step with aiprovider.DefaultBaseURL and the service name in
    // docker-compose.embeddings.yml, or the overlay comes up unconfigured.
    expect(SDK_DEFAULT_BASE.local).toBe('http://embeddings:80/v1')
    // Must stay identical to aiprovider.DefaultBaseURL; internal/messages
    // strips the /v1 back off, because the SDK appends v1/messages itself.
    expect(SDK_DEFAULT_BASE.anthropic).toBe('https://api.anthropic.com/v1')
  })
})

// The form's link is the only place the setup instructions are reachable
// from, so every SDK needs somewhere to send the operator.
describe('providerDocs', () => {
  it('covers every SDK', () => {
    for (const { value } of SDK_OPTIONS) {
      expect(providerDocs(value).href).toMatch(/^\/docs\/[a-z_]+\.html(#[a-z-]+)?$/)
    }
  })

  it('shows no keyless hint for the SDK that signs in', () => {
    expect(keylessProviderHint('chatgpt')).toBe('')
    expect(providerDocs('chatgpt').href).toBe('/docs/chatgpt_login.html')
  })

  it('points each SDK with its own guide there', () => {
    expect(providerDocs('local').href).toBe('/docs/local_embeddings.html')
    expect(providerDocs('docling').href).toBe('/docs/local_ocr.html')
    expect(providerDocs('google_vision').href).toBe('/docs/google_vision.html')
    expect(providerDocs('mistral').href).toBe('/docs/guided_ai_setup.html')
  })

  it('falls back to the provider overview', () => {
    expect(providerDocs('openai').href).toBe('/docs/ai_providers.html#choosing-a-provider')
    expect(providerDocs(undefined).href).toBe('/docs/ai_providers.html#choosing-a-provider')
  })
})


// The bug this guards: a call site pre-filtering with isLLMProvider stripped
// every `local` provider before providerServesPurpose ever ran.
describe('eligibleProviders', () => {
  const all = [
    { id: 'p1', sdk: 'openai' },
    { id: 'p2', sdk: 'google_vision' },
    { id: 'p3', sdk: 'local' },
    { id: 'p4', sdk: 'docling' },
  ]

  it('offers the local sidecar to the embedding picker', () => {
    expect(eligibleProviders(all, 'embedding').map((p) => p.id)).toEqual(['p1', 'p3'])
  })

  it('offers both OCR engines but not the embeddings one', () => {
    expect(eligibleProviders(all, 'ocr').map((p) => p.id)).toEqual(['p1', 'p2', 'p4'])
  })

  it('offers only chat-capable SDKs to an LLM binding', () => {
    expect(eligibleProviders(all, 'llm').map((p) => p.id)).toEqual(['p1'])
  })

  it('keeps an already-bound provider whatever its SDK, so it never renders blank', () => {
    expect(eligibleProviders(all, 'llm', 'p3').map((p) => p.id)).toEqual(['p1', 'p3'])
  })
})

// chatgpt keeps these predicates from collapsing into one "is it a hosted LLM"
// question, as local does from the other side.
describe('the ChatGPT subscription SDK', () => {
  it('chats and reads documents but does not embed', () => {
    expect(isLLMProvider('chatgpt')).toBe(true)
    expect(canEmbedProvider('chatgpt')).toBe(false)
    expect(providerServesPurpose('chatgpt', 'llm')).toBe(true)
    expect(providerServesPurpose('chatgpt', 'ocr')).toBe(true)
    expect(providerServesPurpose('chatgpt', 'embedding')).toBe(false)
  })

  it('signs in instead of taking a key', () => {
    expect(requiresSignIn('chatgpt')).toBe(true)
    expect(requiresAPIKey('chatgpt')).toBe(false)
    for (const sdk of ['openai', 'openrouter', 'mistral', 'opencode', 'google_vision', 'local', 'docling', 'tavily']) {
      expect(requiresSignIn(sdk)).toBe(false)
    }
  })

  it('is configured by its token, not by a key or an address', () => {
    const row = { sdk: 'chatgpt' as const, api_key_set: true, signed_in: false, base_url: 'x' }
    expect(providerConfigured(row)).toBe(false)
    expect(providerConfigured({ ...row, signed_in: true })).toBe(true)
  })

  it('is offered in the SDK picker with a default base URL', () => {
    expect(SDK_OPTIONS.some((option) => option.value === 'chatgpt')).toBe(true)
    expect(SDK_DEFAULT_BASE.chatgpt).toBe('https://chatgpt.com/backend-api/codex')
  })
})

// These are the ids docs/guided_ai_setup.md tells an operator to type, so a
// rename there is a rename here.
describe('recommendedModel', () => {
  it("names Mistral's OCR and embedding models", () => {
    expect(recommendedModel('mistral', 'ocr')).toBe('mistral-ocr-latest')
    expect(recommendedModel('mistral', 'embedding')).toBe('mistral-embed')
  })

  it('names the default extraction model on Opencode', () => {
    expect(recommendedModel('opencode', 'llm')).toBe('gpt-5.6-luna')
  })

  it('names the embedding model the local sidecar serves by default', () => {
    expect(recommendedModel('local', 'embedding')).toBe('BAAI/bge-m3')
    expect(recommendedModel('local', 'ocr')).toBe('')
  })

  // A guess in the wrong box would be saved as a binding to a model the
  // provider does not serve.
  it('suggests nothing for a job an SDK has no known id for', () => {
    expect(recommendedModel('mistral', 'llm')).toBe('')
    expect(recommendedModel('opencode', 'ocr')).toBe('')
    expect(recommendedModel('opencode', 'embedding')).toBe('')
    expect(recommendedModel('openai', 'llm')).toBe('')
    expect(recommendedModel(undefined, 'ocr')).toBe('')
  })
})

// Tavily is the first SDK that serves neither a model nor a document, which
// makes providerServesPurpose's OCR arm the thing to watch: it defaults to
// true, so an SDK missing from it is offered for OCR and fails on the first
// upload.
describe('tavily', () => {
  it('searches the web and does nothing else', () => {
    expect(canWebSearchProvider('tavily')).toBe(true)
    expect(providerServesPurpose('tavily', 'websearch')).toBe(true)
    expect(isLLMProvider('tavily')).toBe(false)
    expect(canEmbedProvider('tavily')).toBe(false)
    expect(providerServesPurpose('tavily', 'llm')).toBe(false)
    expect(providerServesPurpose('tavily', 'embedding')).toBe(false)
    expect(providerServesPurpose('tavily', 'ocr')).toBe(false)
  })

  it('is the only SDK the web-search picker offers', () => {
    for (const sdk of ['openai', 'openrouter', 'mistral', 'opencode', 'chatgpt', 'google_vision', 'local', 'docling']) {
      expect(canWebSearchProvider(sdk)).toBe(false)
      expect(providerServesPurpose(sdk, 'websearch')).toBe(false)
    }
    expect(canWebSearchProvider(undefined)).toBe(false)
  })

  // A binding already made is kept whatever its SDK, so an existing row never
  // renders as blank -- the same rule every other picker follows.
  it('keeps a bound provider in the list even when it no longer serves', () => {
    const providers = [
      { id: 'p1', sdk: 'tavily' },
      { id: 'p2', sdk: 'openai' },
    ]
    expect(eligibleProviders(providers, 'websearch').map((p) => p.id)).toEqual(['p1'])
    expect(eligibleProviders(providers, 'websearch', 'p2').map((p) => p.id)).toEqual(['p1', 'p2'])
  })

  it('is offered in the SDK picker with a default base URL', () => {
    expect(SDK_OPTIONS.some((option) => option.value === 'tavily')).toBe(true)
    expect(SDK_DEFAULT_BASE.tavily).toBe('https://api.tavily.com')
    expect(requiresAPIKey('tavily')).toBe(true)
  })
})

describe('providerOptionLabel', () => {
  it('shows the SDK only when the alias does not already name it', () => {
    expect(providerOptionLabel({ alias: 'Docling', sdk: 'docling' })).toBe('Docling (Local OCR)')
    expect(providerOptionLabel({ alias: 'Local embeddings', sdk: 'local' })).toBe('Local embeddings')
  })
})
