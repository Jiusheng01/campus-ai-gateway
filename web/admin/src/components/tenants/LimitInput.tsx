import {fmt} from '../../lib/format'

/**
 * Renders one of the three states a limit can be in.
 *
 * The three states are the whole point of the hierarchy, so they must not look
 * alike: null is "inherit", 0 is "explicitly unlimited", and a positive number
 * is this tenant's own cap. Printing "0" for null — the obvious `n || 0` — would
 * tell an operator they had set an unlimited cap on a tenant that actually
 * inherits one.
 */
export function limitText(n: number | null | undefined): string {
  if (n === null || n === undefined) return '继承'
  if (n === 0) return '不限'
  return fmt(n)
}

/** A class name that makes the three states distinguishable at a glance. */
export function limitClass(n: number | null | undefined): string {
  if (n === null || n === undefined) return 'limitInherit'
  if (n === 0) return 'limitUnlimited'
  return 'limitSet'
}

/**
 * A tri-state limit input.
 *
 * One control, not two: an empty box means inherit, "0" means unlimited, a
 * positive number is the cap. A checkbox plus a number box would be four
 * states to reason about (unchecked with a number in it, etc.) for a value that
 * has three.
 *
 * The value is held as a string while the user types so that clearing the box
 * can mean "inherit" rather than snapping back to 0, which is the single most
 * likely way to lose the distinction.
 */
export function LimitInput({
  value,
  onChange,
  placeholder,
}: {
  value: number | null | undefined
  onChange: (v: number | null) => void
  placeholder?: string
}) {
  const text = value === null || value === undefined ? '' : String(value)
  return (
    <input
      type="number"
      min="0"
      inputMode="numeric"
      className="mono"
      value={text}
      placeholder={placeholder ?? '留空 = 继承'}
      onChange={e => {
        const raw = e.target.value.trim()
        if (raw === '') {
          onChange(null)
          return
        }
        const n = Number(raw)
        onChange(Number.isFinite(n) && n >= 0 ? Math.floor(n) : 0)
      }}
    />
  )
}
