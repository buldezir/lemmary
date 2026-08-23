import type { ButtonHTMLAttributes } from 'react'
import { accentContrastText } from '../lib/accent'

export const inputClassName =
  'w-full rounded-xs border border-line-strong bg-bright px-3 py-2 text-sm text-ink outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood'
export const labelClassName = 'flex flex-col gap-1'
export const labelTextClassName =
  'text-[11px] font-semibold uppercase tracking-[0.12em] text-ink-soft'
/** Small explanation shown under a form field. */
export const fieldHintClassName = 'text-xs text-ink-soft'
export const sectionClassName = 'border border-line bg-surface p-6'
export const sectionTitleClassName =
  'mb-4 border-b border-line pb-2 font-display text-lg font-semibold text-ink'

const buttonVariantClassName = {
  primary: 'bg-ink text-paper hover:bg-oxblood',
  secondary: 'border border-line-strong bg-surface text-ink hover:border-ink hover:bg-bright',
  danger: 'border border-madder/40 bg-madder/5 text-madder hover:bg-madder/10',
} as const

const buttonSizeClassName = {
  md: 'px-4 py-2 text-sm',
  sm: 'px-3 py-1.5 text-sm',
  xs: 'px-2 py-1 text-xs',
} as const

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: keyof typeof buttonVariantClassName
  size?: keyof typeof buttonSizeClassName
}

/** Shared button. Defaults to type="button" so forms opt into submit explicitly. */
export function Button({
  variant = 'primary',
  size = 'md',
  type = 'button',
  className = '',
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      className={`rounded-xs font-medium tracking-wide transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-oxblood disabled:cursor-not-allowed disabled:opacity-50 ${buttonSizeClassName[size]} ${buttonVariantClassName[variant]} ${className}`}
      {...props}
    />
  )
}

/** App initial on the accent color, used in the header and the gate screens. */
export function AppLogo({ appName, accent }: { appName: string; accent: string }) {
  return (
    <span
      className="flex h-7 w-7 items-center justify-center font-display text-sm font-semibold"
      style={{ backgroundColor: accent, color: accentContrastText(accent) }}
    >
      {appName.trim().charAt(0).toUpperCase() || 'P'}
    </span>
  )
}
