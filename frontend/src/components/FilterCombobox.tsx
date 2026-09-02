import { useId } from 'react'
import { Combobox } from './Combobox'

export type FilterComboboxOption = {
  value: string
  label: string
}

type Props = {
  label: string
  value: string
  options: FilterComboboxOption[]
  allValue?: string
  allLabel: string
  onChange: (value: string) => void
}

/** A searchable filter select with an "All …" entry at the top of the list. */
export function FilterCombobox({
  label,
  value,
  options,
  allValue = 'all',
  allLabel,
  onChange,
}: Props) {
  const inputId = useId()

  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={inputId} className="text-xs font-medium text-ink-soft">
        {label}
      </label>
      <Combobox
        id={inputId}
        value={value}
        options={[{ value: allValue, label: allLabel }, ...options]}
        placeholder={allLabel}
        bgClassName="bg-surface"
        onChange={onChange}
      />
    </div>
  )
}
