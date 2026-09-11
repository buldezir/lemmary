import { describe, expect, it } from 'vitest'

import {
  canEmbedProvider,
  isLLMProvider,
  keylessProviderDocs,
  keylessProviderHint,
  eligibleProviders,
  providerConfigured,
  providerServesPurpose,
  recommendedModel,
  requiresAPIKey,
  requiresSignIn,
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
    // opencode chats but serves no /embeddings, so it is an LLM SDK that is
    // not an embedding one. Mirrors aiprovider.CanEmbed.
    expect(isLLMProvider('opencode')).toBe(true)
    expect(canEmbedProvider('opencode')).toBe(false)
  })

  it('asks for a key everywhere but the two sidecars', () => {
    expect(requiresAPIKey('local')).toBe(false)
    expect(requiresAPIKey('docling')).toBe(false)
    for (const sdk of ['openai', 'openrouter', 'mistral', 'opencode', 'google_vision']) {
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
    // Keep in step with aiprovider.DefaultBaseURL(SDKLocalEmbeddings) and the service
    // name in docker-compose.embeddings.yml, or the overlay comes up
    // unconfigured.
    expect(SDK_DEFAULT_BASE.local).toBe('http://embeddings:80/v1')
  })
})

// The hint under a keyless provider's Base URL is the only place the setup
// instructions are reachable from the form, so every SDK that shows that hint
// has to have somewhere to send the operator.
describe('keylessProviderDocs', () => {
  it('covers every SDK that shows the keyless hint', () => {
    for (const { value } of SDK_OPTIONS) {
      // The hint's own condition, not just `no API key`: chatgpt needs no key
      // either and still shows no hint, because the sign-in panel stands where
      // the hint would. Asking the narrower question here would demand a
      // sidecar guide for an SDK that has no sidecar.
      if (requiresAPIKey(value) || requiresSignIn(value)) continue
      expect(keylessProviderDocs(value)?.href).toBeTruthy()
    }
  })

  // ...and the SDK skipped above must genuinely show no hint, or the exemption
  // would hide a missing link rather than describe one that is not needed.
  it('is not needed for the SDK that signs in', () => {
    expect(keylessProviderHint('chatgpt')).toBe('')
    expect(keylessProviderDocs('chatgpt')).toBeNull()
  })

  it('points each sidecar at its own guide', () => {
    expect(keylessProviderDocs('local')?.href).toBe('/docs/local_embeddings.html')
    expect(keylessProviderDocs('docling')?.href).toBe('/docs/local_ocr.html')
  })

  // The .html matters: VitePress has no cleanUrls and the static handler in
  // appwire/wire.go never tries the suffix, so a bare path lands on the SPA.
  it('has nothing to say about the hosted SDKs', () => {
    expect(keylessProviderDocs('openai')).toBeNull()
    expect(keylessProviderDocs(undefined)).toBeNull()
  })
})


// The bug this guards: Settings built its own `providers.filter(isLLMProvider)`
// list and handed it to every picker including Embeddings, so a `local`
// provider was gone before providerServesPurpose ever ran. Adding one in
// Settings appeared to work and it was simply absent from the Embeddings
// dropdown, with nothing to say why.
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

// chatgpt is what keeps these predicates from collapsing back into one "is it
// a hosted LLM" question: it chats and reads documents but does not embed,
// where local embeds and does nothing else.
describe('the ChatGPT subscription SDK', () => {
  it('chats and reads documents but does not embed', () => {
    expect(isLLMProvider('chatgpt')).toBe(true)
    expect(canEmbedProvider('chatgpt')).toBe(false)
    expect(providerServesPurpose('chatgpt', 'llm')).toBe(true)
    expect(providerServesPurpose('chatgpt', 'ocr')).toBe(true)
    // The Codex backend serves no /embeddings at all, so this is the one
    // binding it cannot take.
    expect(providerServesPurpose('chatgpt', 'embedding')).toBe(false)
  })

  it('signs in instead of taking a key', () => {
    expect(requiresSignIn('chatgpt')).toBe(true)
    expect(requiresAPIKey('chatgpt')).toBe(false)
    for (const sdk of ['openai', 'openrouter', 'mistral', 'opencode', 'google_vision', 'local', 'docling']) {
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

// The wizard prefills these so the guided path reaches the models step already
// bound. They are the ids docs/guided_ai_setup.md tells an operator to type, so
// a rename there is a rename here.
describe('recommendedModel', () => {
  it("names Mistral's OCR and embedding models", () => {
    expect(recommendedModel('mistral', 'ocr')).toBe('mistral-ocr-latest')
    expect(recommendedModel('mistral', 'embedding')).toBe('mistral-embed')
  })

  it('names the default extraction model on Opencode', () => {
    expect(recommendedModel('opencode', 'llm')).toBe('gpt-5.6-luna')
  })

  // A guess in the wrong box is worse than an empty box: it would be saved as a
  // binding to a model the provider does not serve.
  it('suggests nothing for a job an SDK has no known id for', () => {
    expect(recommendedModel('mistral', 'llm')).toBe('')
    expect(recommendedModel('opencode', 'ocr')).toBe('')
    expect(recommendedModel('opencode', 'embedding')).toBe('')
    expect(recommendedModel('openai', 'llm')).toBe('')
    expect(recommendedModel(undefined, 'ocr')).toBe('')
  })
})
