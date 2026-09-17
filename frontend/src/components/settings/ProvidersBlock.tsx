import { type SubmitEvent, useState } from 'react'

import {
  createAIProvider,
  defaultCatalog,
  deleteAIProvider,
  MODEL_CATALOGS,
  requiresAPIKey,
  requiresSignIn,
  sdkAliasDefault,
  sdkLabel,
  updateAIProvider,
  keylessProviderDocs,
  keylessProviderHint,
  SDK_DEFAULT_BASE,
  SDK_OPTIONS,
  type AIProvider,
  type ProviderSDK,
} from '../../lib/api/providers'
import { ChatGPTSignIn } from '../ChatGPTSignIn'
import {
  Button,
  DocsLink,
  inputClassName,
  labelClassName,
  fieldHintClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../ui'

type ProviderDraft = {
  sdk: ProviderSDK
  alias: string
  base_url: string
  api_key: string
  catalog: string
}

function emptyDraft(sdk: ProviderSDK = 'openai'): ProviderDraft {
  return { sdk, alias: '', base_url: SDK_DEFAULT_BASE[sdk], api_key: '', catalog: defaultCatalog(sdk) }
}

/**
 * The add/edit drawer is a form of its own, saved against the providers API
 * rather than the settings record, so it reports back to the tab's dialog.
 */
export function ProvidersBlock({
  providers,
  onChanged,
  onError,
  onSuccess,
}: {
  providers: AIProvider[]
  /** Re-reads the list after a create, update, delete or sign-in. */
  onChanged: () => void | Promise<unknown>
  onError: (message: string) => void
  onSuccess: (message: string) => void
}) {
  const [draft, setDraft] = useState<ProviderDraft>(emptyDraft())
  const [editingId, setEditingId] = useState<string | null>(null)
  const [showAdd, setShowAdd] = useState(false)
  const keylessDocs = keylessProviderDocs(draft.sdk)

  async function onSaveProvider(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      onError('')
      onSuccess('')
      if (editingId) {
        await updateAIProvider(editingId, {
          sdk: draft.sdk,
          alias: draft.alias.trim(),
          base_url: draft.base_url.trim(),
          catalog: draft.catalog,
          ...(draft.api_key.trim() ? { api_key: draft.api_key.trim() } : {}),
        })
      } else {
        await createAIProvider({
          sdk: draft.sdk,
          alias: draft.alias.trim() || sdkAliasDefault(draft.sdk),
          base_url: draft.base_url.trim(),
          api_key: draft.api_key.trim(),
          catalog: draft.catalog,
        })
      }
      await onChanged()
      setDraft(emptyDraft())
      setEditingId(null)
      setShowAdd(false)
      onSuccess('Provider saved.')
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Failed to save provider')
    }
  }

  async function onDeleteProvider(id: string) {
    try {
      onError('')
      onSuccess('')
      await deleteAIProvider(id)
      await onChanged()
      onSuccess('Provider deleted.')
    } catch (err) {
      onError(err instanceof Error ? err.message : 'Failed to delete provider')
    }
  }

  return (
    <section className={`${sectionClassName} mb-5`}>
      <h2 className={sectionTitleClassName}>Providers</h2>
      <ul className="mb-4 flex flex-col gap-2">
        {providers.length === 0 && <li className="text-sm text-ink-soft">No providers yet.</li>}
        {providers.map((item) => (
          <li
            key={item.id}
            className="flex flex-wrap items-center justify-between gap-2 rounded-xs border border-line bg-bright px-3 py-2"
          >
            <div>
              <p className="text-sm font-medium text-ink">{item.alias}</p>
              <p className="text-xs text-ink-soft">
                {sdkLabel(item.sdk)}
                {item.base_url ? ` · ${item.base_url}` : ''}
                {requiresSignIn(item.sdk)
                  ? item.signed_in
                    ? ' · signed in'
                    : ' · not signed in'
                  : requiresAPIKey(item.sdk)
                    ? item.api_key_set
                      ? ' · key set'
                      : ' · missing key'
                    : ' · no key needed'}
              </p>
              {/* On the saved row rather than in the add form: the flow needs a
                  provider id to store the token against. */}
              {requiresSignIn(item.sdk) && <ChatGPTSignIn provider={item} onChange={onChanged} />}
            </div>
            <div className="flex gap-2">
              <Button
                variant="secondary"
                size="xs"
                onClick={() => {
                  setEditingId(item.id)
                  setShowAdd(true)
                  setDraft({
                    sdk: item.sdk,
                    alias: item.alias,
                    base_url: item.base_url,
                    api_key: '',
                    catalog: item.catalog ?? defaultCatalog(item.sdk),
                  })
                }}
              >
                Edit
              </Button>
              <Button
                variant="secondary"
                size="xs"
                className="text-madder hover:bg-madder/10"
                onClick={() => void onDeleteProvider(item.id)}
              >
                Delete
              </Button>
            </div>
          </li>
        ))}
      </ul>
      {!showAdd ? (
        <Button
          variant="secondary"
          size="sm"
          onClick={() => {
            setEditingId(null)
            setDraft(emptyDraft())
            setShowAdd(true)
          }}
        >
          Add provider
        </Button>
      ) : (
        <form className="grid gap-3 sm:grid-cols-2" onSubmit={onSaveProvider}>
          <label className={labelClassName}>
            <span className={labelTextClassName}>SDK</span>
            <select
              className={inputClassName}
              value={draft.sdk}
              onChange={(event) => {
                const sdk = event.target.value as ProviderSDK
                setDraft((current) => ({
                  ...current,
                  sdk,
                  base_url:
                    current.base_url === SDK_DEFAULT_BASE[current.sdk]
                      ? SDK_DEFAULT_BASE[sdk]
                      : current.base_url,
                  catalog:
                    current.catalog === defaultCatalog(current.sdk)
                      ? defaultCatalog(sdk)
                      : current.catalog,
                  // The key field is about to disappear; a value typed before
                  // the switch would otherwise be posted invisibly.
                  api_key: requiresAPIKey(sdk) ? current.api_key : '',
                }))
              }}
            >
              {SDK_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </label>
          <label className={labelClassName}>
            <span className={labelTextClassName}>Alias</span>
            <input
              className={inputClassName}
              value={draft.alias}
              placeholder={sdkAliasDefault(draft.sdk)}
              onChange={(event) => setDraft((current) => ({ ...current, alias: event.target.value }))}
            />
          </label>
          <label className={labelClassName}>
            <span className={labelTextClassName}>Model catalogue</span>
            <select
              className={inputClassName}
              value={draft.catalog}
              onChange={(event) =>
                setDraft((current) => ({ ...current, catalog: event.target.value }))
              }
            >
              <option value="">None — no context window shown</option>
              {MODEL_CATALOGS.map((id) => (
                <option key={id} value={id}>
                  {id}
                </option>
              ))}
            </select>
          </label>
          {draft.sdk !== 'google_vision' && (
            <label className={`${labelClassName} sm:col-span-2`}>
              <span className={labelTextClassName}>Base URL</span>
              <input
                className={inputClassName}
                value={draft.base_url}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, base_url: event.target.value }))
                }
              />
            </label>
          )}
          {requiresAPIKey(draft.sdk) ? (
            <label className={`${labelClassName} sm:col-span-2`}>
              <span className={labelTextClassName}>
                API key{editingId ? ' (leave blank to keep)' : ''}
              </span>
              <input
                type="password"
                autoComplete="off"
                className={inputClassName}
                value={draft.api_key}
                required={!editingId}
                onChange={(event) =>
                  setDraft((current) => ({ ...current, api_key: event.target.value }))
                }
              />
            </label>
          ) : requiresSignIn(draft.sdk) ? (
            <p className={`${fieldHintClassName} sm:col-span-2`}>
              Save the provider first, then sign in to it from the list above. Chat,
              extraction and Deep Search can run on the subscription; embeddings and OCR
              cannot, and keep whichever provider they have.
            </p>
          ) : (
            <p className={`${fieldHintClassName} sm:col-span-2`}>
              {keylessProviderHint(draft.sdk)}{' '}
              {keylessDocs && (
                <DocsLink href={keylessDocs.href}>Read the {keylessDocs.label} guide.</DocsLink>
              )}
            </p>
          )}
          <div className="flex gap-2 sm:col-span-2">
            <Button type="submit" size="sm">
              {editingId ? 'Update provider' : 'Save provider'}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setShowAdd(false)
                setEditingId(null)
              }}
            >
              Cancel
            </Button>
          </div>
        </form>
      )}
    </section>
  )
}
