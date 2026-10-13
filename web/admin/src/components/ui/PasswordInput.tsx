import {useState} from 'react'
import {Eye, EyeOff} from 'lucide-react'

type Props = {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  autoComplete?: string
  autoFocus?: boolean
  id?: string
}

/**
 * Password field with a show/hide toggle.
 *
 * The toggle is icon-only, so it carries both an aria-label and a title —
 * otherwise a screen reader announces nothing useful.
 */
export function PasswordInput({value, onChange, placeholder, autoComplete, autoFocus, id}: Props) {
  const [visible, setVisible] = useState(false)
  const label = visible ? '隐藏密码' : '显示密码'
  return (
    <div className="passwordWrap">
      <input
        id={id}
        type={visible ? 'text' : 'password'}
        value={value}
        placeholder={placeholder}
        autoComplete={autoComplete}
        autoFocus={autoFocus}
        onChange={e => onChange(e.target.value)}
      />
      <button type="button" className="revealBtn" onClick={() => setVisible(v => !v)} aria-label={label} title={label}>
        {visible ? <EyeOff /> : <Eye />}
      </button>
    </div>
  )
}
