import {type ReactNode} from 'react'
import {CampusBannerIllustration} from './CampusIllustrations'

type PageBannerProps = {
  title: ReactNode
  subtitle: ReactNode
  badge?: ReactNode
  actions?: ReactNode
  illustration?: boolean
  className?: string
}

export function PageBanner({
  title,
  subtitle,
  badge,
  actions,
  illustration = false,
  className = '',
}: PageBannerProps) {
  return (
    <section className={`pageBanner ${className}`.trim()}>
      <div className="pageBannerContent">
        <div className="pageBannerHead">
          <h2 className="pageBannerTitle">{title}</h2>
          {badge && <span className="pageBannerBadge">{badge}</span>}
        </div>
        <p className="pageBannerSub">{subtitle}</p>
        {actions && <div className="pageBannerActions">{actions}</div>}
      </div>
      {illustration && (
        <div className="pageBannerIllustrationWrap" aria-hidden="true">
          <CampusBannerIllustration className="pageBannerIllustration" />
        </div>
      )}
    </section>
  )
}
