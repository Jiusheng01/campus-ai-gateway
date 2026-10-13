import type {ReactNode} from 'react'

type Props = {
  label: string
  hint?: string
  children: ReactNode
  htmlFor?: string
}

/**
 * Form row. The label wraps the control so the association holds without
 * threading ids through every form.
 */
export function Field({label, hint, children, htmlFor}: Props) {
  return (
    <label className="field" htmlFor={htmlFor}>
      <span className="fieldLabel">{label}</span>
      {children}
      {hint && <small className="fieldHint">{hint}</small>}
    </label>
  )
}

export function Notice({tone = 'info', children}: {tone?: 'info' | 'warning'; children: ReactNode}) {
  return <div className={`notice ${tone}`}>{children}</div>
}

export function Form({children, onSubmit, actions}: {children: ReactNode; onSubmit: () => void; actions?: ReactNode}) {
  return (
    <form
      className="form"
      onSubmit={e => {
        e.preventDefault()
        onSubmit()
      }}
    >
      {children}
      <div className="formActions">{actions ?? <button className="btn primary md" type="submit">保存</button>}</div>
    </form>
  )
}
