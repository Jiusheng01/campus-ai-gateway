import {useEffect, useState} from 'react'
import {fmt} from '../../lib/format'

/**
 * Text for one of the three states a limit can be in.
 *
 * The distinction is the whole point of the hierarchy, so the table must not
 * print the same thing for two of them: null is "inherit" (an ancestor
 * decides), 0 is "explicitly unlimited" (nobody decides), and a positive
 * number is this tenant's own cap.
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
 * states to reason about for a value that has three.
 *
 * The box keeps its own text while typing and only reports the parsed number
 * upwards. Rendering `String(value)` straight from the prop rewrites the box on
 * every keystroke, which drops leading input and moves the caret.
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

  // Adopt a value that came from elsewhere without disturbing what is being
  // typed: leave the box alone when it already represents this number.
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
        if (raw !== '' && !/^\d+$/.test(raw)) return
        setText(raw)
        onChange(raw === '' ? null : Number(raw))
      }}
    />
  )
}
