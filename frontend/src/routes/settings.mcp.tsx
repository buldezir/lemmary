import { type SubmitEvent } from 'react'
import { Link } from '@tanstack/react-router'

import { useAsync } from '../hooks/useAsync'
import { useSettingsForm } from '../hooks/useSettingsForm'
import { getMCPStatus } from '../lib/api/mcp'
import { mcpCapabilities } from '../lib/api/settings'
import {
  ResultDialog,
  SaveSettingsButton,
  SettingsLoading,
} from '../components/settings/SettingsFeedback'
import {
  DocsLink,
  fieldHintClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'
import { docsUrl, t, tNode } from '../i18n'

const capabilityText = {
  edit: ['settingsMcp.edit', 'settingsMcp.editHint'],
  reprocess: ['settingsMcp.reprocess', 'settingsMcp.reprocessHint'],
  upload: ['settingsMcp.upload', 'settingsMcp.uploadHint'],
  delete: ['settingsMcp.delete', 'settingsMcp.deleteHint'],
  tags: ['settingsMcp.tags', 'settingsMcp.tagsHint'],
} as const

export function SettingsMCPPage() {
  const { data: status } = useAsync(getMCPStatus, [])
  const { form, loading, error, success, saving, updateField, save, closeResult } =
    useSettingsForm((settings) => ({ mcp_capabilities: settings.mcp_capabilities ?? [] }))

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return
    await save({ mcp_capabilities: form.mcp_capabilities })
  }

  if (loading || !form) return <SettingsLoading error={error} />

  return (
    <form onSubmit={onSubmit}>
      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>{t('settingsMcp.title')}</h2>
        <p className="mb-4 text-sm text-ink-soft">
          {tNode('settingsMcp.intro', {
            link: <DocsLink href={docsUrl('mcp.html')}>{t('account.mcpLink')}</DocsLink>,
          })}
        </p>
        <p className="mb-4 text-sm text-ink-soft">
          {tNode('settingsMcp.connect', {
            link: (
              <Link to="/account" hash="agents" className="underline hover:text-oxblood">
                {t('settingsMcp.accountLink')}
              </Link>
            ),
          })}
        </p>
        {status?.enabled === false && (
          <p className="mb-4 text-sm text-ink-soft">
            {tNode('account.mcpOff', { env: <code>MCP_ENABLED=0</code> })}
          </p>
        )}
        <div className="grid gap-3">
          {mcpCapabilities.map((capability) => (
            <div key={capability} className="rounded-xs border border-line-strong bg-bright p-4">
              <label className="flex items-center gap-2.5 text-sm font-medium text-ink">
                <input
                  type="checkbox"
                  className="h-4 w-4 accent-oxblood"
                  checked={form.mcp_capabilities.includes(capability)}
                  onChange={(e) =>
                    updateField(
                      'mcp_capabilities',
                      e.target.checked
                        ? [...form.mcp_capabilities, capability]
                        : form.mcp_capabilities.filter((c) => c !== capability),
                    )
                  }
                />
                {t(capabilityText[capability][0])}
              </label>
              <p className={`${fieldHintClassName} mt-2`}>{t(capabilityText[capability][1])}</p>
            </div>
          ))}
        </div>
        <div className="mt-4">
          <SaveSettingsButton saving={saving} />
        </div>
      </section>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </form>
  )
}
