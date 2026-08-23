import type { ButtonHTMLAttributes } from 'react'
import { accentContrastText } from '../lib/accent'

export const inputClassName =
  'w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm outline-none placeholder:text-stone-400 focus:border-gray-900 focus:ring-1 focus:ring-gray-900'
export const labelClassName = 'flex flex-col gap-1'
export const labelTextClassName = 'text-xs font-medium text-stone-500'
export const sectionClassName = 'rounded-lg border border-stone-200 bg-stone-50 p-5'
export const sectionTitleClassName = 'mb-4 text-sm font-semibold text-stone-950'

const buttonVariantClassName = {
  primary: 'bg-gray-900 text-white hover:bg-gray-700',
  secondary: 'border border-stone-300 bg-white text-stone-950 hover:bg-stone-100',
  danger: 'border border-red-300 bg-red-50 text-red-700 hover:bg-red-100',
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
      className={`rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${buttonSizeClassName[size]} ${buttonVariantClassName[variant]} ${className}`}
      {...props}
    />
  )
}

/** App initial on the accent color, used in the header and the gate screens. */
export function AppLogo({ appName, accent }: { appName: string; accent: string }) {
  return (
    <span
      className="flex h-7 w-7 items-center justify-center rounded-md text-sm"
      style={{ backgroundColor: accent, color: accentContrastText(accent) }}
    >
      {appName.trim().charAt(0).toUpperCase() || 'P'}
    </span>
  )
}
