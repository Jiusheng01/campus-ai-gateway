import {useEffect, useRef, type ReactNode} from 'react'
import {X} from 'lucide-react'
import {IconButton} from './Button'

type Props = {
  title: ReactNode
  subtitle?: ReactNode
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  /** Dangerous confirmations keep the scrim inert so a stray click is harmless. */
  dismissable?: boolean
}

/** Centred dialog, used where the content deserves the middle of the screen. */
export function Modal({title, subtitle, onClose, children, footer, dismissable = true}: Props) {
  const panelRef = useRef<HTMLDivElement>(null)
  const restoreTo = useRef<HTMLElement | null>(null)

  useEffect(() => {
    restoreTo.current = document.activeElement as HTMLElement | null
    panelRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && dismissable) onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      restoreTo.current?.focus?.()
    }
  }, [onClose, dismissable])

  return (
    <div className="overlay center" onMouseDown={e => e.target === e.currentTarget && dismissable && onClose()}>
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={typeof title === 'string' ? title : undefined}
        tabIndex={-1}
        ref={panelRef}
        onMouseDown={e => e.stopPropagation()}
      >
        <header className="modalHead">
          <div className="modalHeadText">
            <h2 className="modalTitle">{title}</h2>
            {subtitle && <p className="modalSub">{subtitle}</p>}
          </div>
          <IconButton label="关闭" icon={<X />} onClick={onClose} />
        </header>
        <div className="modalBody">{children}</div>
        {footer && <footer className="modalFoot">{footer}</footer>}
      </div>
    </div>
  )
}
