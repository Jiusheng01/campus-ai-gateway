import type {ReactNode} from 'react'

/**
 * One filter cell shared by the administrator request-log page and the
 * personal console's log page.
 *
 * Every control carries its label rather than relying on a placeholder: the
 * placeholders disappear as soon as a value is typed, which leaves a row of
 * anonymous boxes, and a `datetime-local` input shows no placeholder at all —
 * two of them side by side are indistinguishable without one. Wrapping the
 * input in the label also makes the caption a click target for it.
 */
export function LogFilterField({label, children}: {label: string; children: ReactNode}) {
  return (
    <label className="filterField">
      <span>{label}</span>
      {children}
    </label>
  )
}
