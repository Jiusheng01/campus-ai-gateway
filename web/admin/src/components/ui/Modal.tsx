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
  const closeRef = useRef(onClose)
  closeRef.current = onClose

  useEffect(() => {
    restoreTo.current = document.activeElement as HTMLElement | null
    panelRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && dismissable) closeRef.current()
      if (e.key !== 'Tab') return
      const panel = panelRef.current
      if (!panel) return
      // The dialog is modal, so Tab keeps cycling within the panel instead of
      // reaching controls behind the scrim. Only focusable descendants count;
      // the panel itself is a focus target (tabIndex -1) but not in the cycle.
      const focusable = Array.from(
        panel.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      )
      if (!focusable.length) {
        e.preventDefault()
        panel.focus()
        return
      }
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      const active = document.activeElement
      // Focus outside the panel (or nowhere) is pulled back in, so the page
      // behind the scrim never becomes the tab sequence.
      if (!active || !panel.contains(active)) {
        e.preventDefault()
        ;(e.shiftKey ? last : first).focus()
        return
      }
      if (e.shiftKey ? active === first : active === last) {
        e.preventDefault()
        ;(e.shiftKey ? last : first).focus()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      restoreTo.current?.focus?.()
    }
  }, [dismissable])

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
