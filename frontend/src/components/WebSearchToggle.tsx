import { useAppMeta } from '../hooks/useAppMeta'

/**
 * Lets one chat reach the public web. Renders nothing unless an operator has
 * bound a web-search provider, so the usual state of this control is absent.
 *
 * Deliberately not remembered: the flag rides each request rather than the
 * conversation, so a reload starts from off. The tools are billed per call, and
 * off is the safe direction to forget in.
 */
export function WebSearchToggle({
  checked,
  onChange,
  disabled,
}: {
  checked: boolean
  onChange: (next: boolean) => void
  disabled?: boolean
}) {
  const { webSearch } = useAppMeta()
  if (!webSearch) return null

  return (
    <label className="flex cursor-pointer items-start gap-2 text-sm text-ink">
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        className="mt-0.5 cursor-pointer disabled:cursor-not-allowed"
      />
      <span>
        Search the web
        <span className="ml-2 text-xs text-ink-muted">
          Looks things up online when your archive cannot answer. Costs a web-search credit
          per lookup.
        </span>
      </span>
    </label>
  )
}
