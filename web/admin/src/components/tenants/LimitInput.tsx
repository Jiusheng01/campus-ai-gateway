import {useEffect, useState} from 'react'
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

const toText = (v: number | null | undefined) => (v === null || v === undefined ? '' : String(v))

/**
 * A tri-state limit input.
 *
 * One control, not two: an empty box means inherit, "0" means unlimited, a
 * positive number is the cap. A checkbox plus a number box would be four
 * states to reason about (unchecked with a number in it, etc.) for a value that
 * has three.
 *
 * The box keeps its own text while the user types, and only reports the parsed
 * number upwards. Rendering `String(value)` straight from the prop looked
 * simpler but rewrote the box on every keystroke: a half-typed "10" was
 * normalised, leading zeros were dropped and the caret jumped, so a cap like
 * 1000000 could not be typed in one go.
 *
 * `type="text"` with a digits-only guard, not `type="number"`: a number input
 * accepts "-", "e" and ".", all of which are meaningless for a token cap, and
 * its spinner changes the value on a stray scroll wheel — which is exactly the
 * kind of silent edit this screen must not allow.
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
  const [text, setText] = useState(() => toText(value))

  // Adopt a value that came from somewhere else (opening another row, a reset
  // after save, a server-side clamp) without disturbing what is being typed:
  // if the box already represents this number, leave the box alone.
  useEffect(() => {
    setText(current => {
      const asNumber = current === '' ? null : Number(current)
      const incoming = value === null || value === undefined ? null : value
      return asNumber === incoming ? current : toText(incoming)
    })
  }, [value])

  return (
    <input
      type="text"
      inputMode="numeric"
      autoComplete="off"
      className="mono"
      value={text}
      placeholder={placeholder ?? '留空 = 继承'}
      onChange={e => {
        const raw = e.target.value.trim()
        if (raw !== '' && !/^\d+$/.test(raw)) return // digits only: no sign, no exponent, no separator
        setText(raw)
        onChange(raw === '' ? null : Number(raw))
      }}
    />
  )
}
