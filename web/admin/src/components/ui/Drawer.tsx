import {useEffect, useRef, type ReactNode} from 'react'
import {X} from 'lucide-react'
import {IconButton} from './Button'

type Props = {
  title: ReactNode
  subtitle?: ReactNode
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  /** Set false for flows that must not be dismissed by an accidental click. */
  dismissable?: boolean
  size?: 'md' | 'lg'
}

/**
 * Right-hand side panel used for create/edit forms and record details.
 *
 * Escape closes it, clicking the scrim closes it, and focus moves into the
 * panel on open and returns to the previously focused element on close.
 */
export function Drawer({title, subtitle, onClose, children, footer, dismissable = true, size = 'md'}: Props) {
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
    <div className="overlay" onMouseDown={e => e.target === e.currentTarget && dismissable && onClose()}>
      <aside
        className={`drawer ${size}`}
        role="dialog"
        aria-modal="true"
        aria-label={typeof title === 'string' ? title : undefined}
        tabIndex={-1}
        ref={panelRef}
        onMouseDown={e => e.stopPropagation()}
      >
        <header className="drawerHead">
          <div className="drawerHeadText">
            <h2 className="drawerTitle">{title}</h2>
            {subtitle && <p className="drawerSub">{subtitle}</p>}
          </div>
          <IconButton label="关闭" icon={<X />} onClick={onClose} />
        </header>
        <div className="drawerBody">{children}</div>
        {footer && <footer className="drawerFoot">{footer}</footer>}
      </aside>
    </div>
  )
}
