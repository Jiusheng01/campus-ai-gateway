import {type SVGProps} from 'react'

/**
 * Campus architecture SVG illustration for headers and hero sections.
 * Clean, lightweight, professional modern tech-campus vector.
 */
export function CampusBannerIllustration(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 460 160"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      {...props}
    >
      <defs>
        <linearGradient id="skyGrad" x1="0" y1="0" x2="460" y2="160" gradientUnits="userSpaceOnUse">
          <stop stopColor="var(--primary)" stopOpacity="0.08" />
          <stop offset="1" stopColor="var(--primary)" stopOpacity="0.01" />
        </linearGradient>
        <linearGradient id="bldgGrad1" x1="200" y1="30" x2="200" y2="150" gradientUnits="userSpaceOnUse">
          <stop stopColor="var(--primary)" stopOpacity="0.18" />
          <stop offset="1" stopColor="var(--primary)" stopOpacity="0.06" />
        </linearGradient>
        <linearGradient id="bldgGrad2" x1="320" y1="50" x2="320" y2="150" gradientUnits="userSpaceOnUse">
          <stop stopColor="var(--primary)" stopOpacity="0.25" />
          <stop offset="1" stopColor="var(--primary)" stopOpacity="0.08" />
        </linearGradient>
        <linearGradient id="accentGrad" x1="0" y1="0" x2="1" y2="0">
          <stop stopColor="var(--primary)" stopOpacity="0.35" />
          <stop offset="1" stopColor="#06b6d4" stopOpacity="0.2" />
        </linearGradient>
      </defs>

      {/* Background soft hills / ground */}
      <path
        d="M0 148 C 120 135, 260 152, 460 140 L 460 160 L 0 160 Z"
        fill="url(#skyGrad)"
      />

      {/* Distant campus library dome */}
      <ellipse cx="140" cy="95" rx="36" ry="24" fill="var(--primary)" fillOpacity="0.08" />
      <rect x="115" y="95" width="50" height="45" rx="2" fill="var(--primary)" fillOpacity="0.08" />
      <line x1="140" y1="65" x2="140" y2="72" stroke="var(--primary)" strokeOpacity="0.2" strokeWidth="2" />

      {/* Main modern academic building */}
      <rect x="180" y="48" width="86" height="96" rx="4" fill="url(#bldgGrad1)" />
      {/* Windows grid on main building */}
      <rect x="190" y="58" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />
      <rect x="208" y="58" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />
      <rect x="226" y="58" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />
      <rect x="244" y="58" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />

      <rect x="190" y="74" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />
      <rect x="208" y="74" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />
      <rect x="226" y="74" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />
      <rect x="244" y="74" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />

      <rect x="190" y="90" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />
      <rect x="208" y="90" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />
      <rect x="226" y="90" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />
      <rect x="244" y="90" width="12" height="10" rx="1.5" fill="#fff" fillOpacity="0.6" />

      {/* Architectural roof trellis / antenna */}
      <line x1="180" y1="48" x2="266" y2="48" stroke="var(--primary)" strokeOpacity="0.3" strokeWidth="2" />
      <line x1="223" y1="36" x2="223" y2="48" stroke="var(--primary)" strokeOpacity="0.4" strokeWidth="2" />
      <circle cx="223" cy="34" r="3" fill="var(--primary)" fillOpacity="0.4" />

      {/* Science & Tech tower */}
      <rect x="280" y="24" width="70" height="120" rx="5" fill="url(#bldgGrad2)" />
      {/* Tower glass facade bands */}
      <rect x="288" y="34" width="54" height="6" rx="1" fill="#fff" fillOpacity="0.5" />
      <rect x="288" y="46" width="54" height="6" rx="1" fill="#fff" fillOpacity="0.5" />
      <rect x="288" y="58" width="54" height="6" rx="1" fill="#fff" fillOpacity="0.5" />
      <rect x="288" y="70" width="54" height="6" rx="1" fill="#fff" fillOpacity="0.5" />
      <rect x="288" y="82" width="54" height="6" rx="1" fill="#fff" fillOpacity="0.5" />
      <rect x="288" y="94" width="54" height="6" rx="1" fill="#fff" fillOpacity="0.5" />
      <rect x="288" y="106" width="54" height="6" rx="1" fill="#fff" fillOpacity="0.5" />

      {/* Clock / Beacon tower spire */}
      <polygon points="365,60 380,26 395,60" fill="var(--primary)" fillOpacity="0.14" />
      <rect x="367" y="60" width="26" height="84" rx="2" fill="var(--primary)" fillOpacity="0.12" />
      <circle cx="380" cy="74" r="5" fill="#fff" fillOpacity="0.7" />

      {/* Campus trees / greenery */}
      <circle cx="100" cy="136" r="14" fill="#10b981" fillOpacity="0.18" />
      <circle cx="116" cy="138" r="10" fill="#10b981" fillOpacity="0.22" />
      <circle cx="272" cy="138" r="12" fill="#10b981" fillOpacity="0.2" />
      <circle cx="410" cy="136" r="16" fill="#10b981" fillOpacity="0.18" />
      <circle cx="425" cy="140" r="11" fill="#10b981" fillOpacity="0.22" />

      {/* AI Network nodes overlay */}
      <circle cx="320" cy="20" r="3.5" fill="var(--primary)" fillOpacity="0.5" />
      <circle cx="260" cy="28" r="2.5" fill="#06b6d4" fillOpacity="0.5" />
      <circle cx="380" cy="16" r="3" fill="var(--primary)" fillOpacity="0.4" />
      <line x1="320" y1="20" x2="260" y2="28" stroke="url(#accentGrad)" strokeWidth="1.2" strokeDasharray="3 3" />
      <line x1="320" y1="20" x2="380" y2="16" stroke="url(#accentGrad)" strokeWidth="1.2" strokeDasharray="3 3" />
    </svg>
  )
}

/**
 * Illustrated promo card at the bottom of the sidebar.
 * "AI 赋能校园 让智慧触手可及"
 */
export function CampusSidebarCard() {
  return (
    <div className="campusPromoCard" role="complementary" aria-label="校园 AI 赋能">
      <div className="campusPromoVisual" aria-hidden="true">
        <svg viewBox="0 0 200 68" fill="none" xmlns="http://www.w3.org/2000/svg" className="campusPromoIllustration">
          <ellipse cx="40" cy="54" rx="28" ry="14" fill="var(--primary)" fillOpacity="0.12" />
          <rect x="24" y="32" width="32" height="26" rx="2" fill="var(--primary)" fillOpacity="0.16" />
          <rect x="80" y="16" width="46" height="44" rx="3" fill="var(--primary)" fillOpacity="0.22" />
          <polygon points="144,32 156,12 168,32" fill="var(--primary)" fillOpacity="0.18" />
          <rect x="146" y="32" width="20" height="28" rx="2" fill="var(--primary)" fillOpacity="0.16" />
          <circle cx="156" cy="38" r="3" fill="#fff" fillOpacity="0.7" />
          <circle cx="178" cy="52" r="8" fill="#10b981" fillOpacity="0.24" />
          <circle cx="16" cy="52" r="7" fill="#10b981" fillOpacity="0.22" />
          <path d="M0 60 Q 100 52 200 60 L 200 68 L 0 68 Z" fill="var(--primary)" fillOpacity="0.08" />
        </svg>
      </div>
      <div className="campusPromoText">
        <b className="campusPromoTitle">AI 赋能校园</b>
        <span className="campusPromoSub">让智慧触手可及</span>
      </div>
    </div>
  )
}

/**
 * Mini sparkline component for KPI cards and table cells.
 * Renders small vertical bars or area line.
 */
export function MiniSparkline({
  data = [35, 45, 30, 60, 75, 55, 80, 95],
  color = 'var(--primary)',
  trend = 'up',
  type = 'bar',
  height = 28,
  width = 64,
}: {
  data?: number[]
  color?: string
  trend?: 'up' | 'down' | 'neutral'
  type?: 'bar' | 'line'
  height?: number
  width?: number
}) {
  const max = Math.max(1, ...data)
  const min = Math.min(...data)
  const range = max - min || 1

  if (type === 'bar') {
    const barWidth = Math.max(3, Math.floor((width - (data.length - 1) * 2) / data.length))
    return (
      <svg
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        fill="none"
        aria-hidden="true"
        className="miniSparkline"
      >
        {data.map((val, idx) => {
          const barH = Math.max(3, Math.round((val / max) * (height - 4)))
          const x = idx * (barWidth + 2)
          const y = height - barH
          const isLast = idx === data.length - 1
          return (
            <rect
              key={idx}
              x={x}
              y={y}
              width={barWidth}
              height={barH}
              rx={1.5}
              fill={color}
              fillOpacity={isLast ? 0.95 : 0.28 + (idx / data.length) * 0.45}
            />
          )
        })}
      </svg>
    )
  }

  // Line / area sparkline
  const points = data.map((val, idx) => {
    const x = (idx / (data.length - 1)) * (width - 4) + 2
    const y = height - 4 - ((val - min) / range) * (height - 8)
    return `${x},${y}`
  })
  const pathD = `M ${points.join(' L ')}`
  const areaD = `M 2,${height} L ${points.join(' L ')} L ${width - 2},${height} Z`

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      fill="none"
      aria-hidden="true"
      className="miniSparkline"
    >
      <path d={areaD} fill={color} fillOpacity="0.12" />
      <path d={pathD} stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}
