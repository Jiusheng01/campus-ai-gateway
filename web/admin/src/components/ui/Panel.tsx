import type {ReactNode} from 'react'

type Props = {
  title: ReactNode
  sub?: ReactNode
  actions?: ReactNode
  footer?: ReactNode
  children: ReactNode
  className?: string
}

export function Panel({title, sub, actions, footer, children, className = ''}: Props) {
  return (
    <section className={`panel ${className}`.trim()}>
      <header className="panelHead">
        <div className="panelHeadText">
          <h2 className="panelTitle">{title}</h2>
          {sub && <p className="panelSub">{sub}</p>}
        </div>
        {actions && <div className="panelActions">{actions}</div>}
      </header>
      <div className="panelBody">{children}</div>
      {footer && <footer className="panelFoot">{footer}</footer>}
    </section>
  )
}
