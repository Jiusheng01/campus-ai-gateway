import type {ButtonHTMLAttributes, ReactNode} from 'react'
import {Loader2} from 'lucide-react'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger'
export type ButtonSize = 'sm' | 'md'

type Props = {
  variant?: ButtonVariant
  size?: ButtonSize
  loading?: boolean
  icon?: ReactNode
  children?: ReactNode
} & Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'>

/**
 * The single button primitive. Every action in the console goes through it so
 * height, radius and weight stay identical across pages.
 */
export function Button({
  variant = 'secondary',
  size = 'md',
  loading = false,
  icon,
  children,
  disabled,
  className = '',
  type = 'button',
  ...rest
}: Props) {
  return (
    <button
      {...rest}
      type={type}
      className={`btn ${variant} ${size} ${className}`.trim()}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
    >
      {loading ? <Loader2 className="spin" aria-hidden /> : icon}
      {children}
    </button>
  )
}

/** Icon-only button. Always carries an accessible name. */
export function IconButton({
  label,
  icon,
  className = '',
  ...rest
}: {label: string; icon: ReactNode} & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button {...rest} type="button" className={`iconBtn ${className}`.trim()} title={label} aria-label={label}>
      {icon}
    </button>
  )
}
