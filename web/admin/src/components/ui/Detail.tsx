import type {ReactNode} from 'react'
import {fmt, orUnknown} from '../../lib/format'

/** Grouped key/value block used by every detail drawer. */
export function Detail({title, rows}: {title: string; rows: [string, ReactNode][]}) {
  return (
    <section className="detailBox">
      <h3>{title}</h3>
      {rows.map(([k, v], i) => (
        <div className="detailRow" key={`${k}-${i}`}>
          <span className="detailKey">{k}</span>
          <b className={`detailValue ${typeof v === 'string' && v.length > 34 ? 'mono wrap' : ''}`.trim()}>
            {v === null || v === undefined ? <span className="unknown">未知</span> : v}
          </b>
        </div>
      ))}
    </section>
  )
}

/**
 * Detail row for telemetry values that may legitimately be missing. Zero and
 * false are real values; only an absent value reads as 未知.
 */
export function TelemetryRow(label: string, value: unknown): [string, ReactNode] {
  return [label, <span key={label} className={value === undefined || value === null || value === '' ? 'unknown' : undefined}>{orUnknown(value)}</span>]
}

export function Progress({label, used, limit}: {label: string; used: number; limit: number}) {
  const pct = limit ? Math.min(100, (used / limit) * 100) : 0
  const tone = pct >= 90 ? 'danger' : pct >= 70 ? 'warning' : 'primary'
  return (
    <div className="progress">
      <div className="progressHead">
        <span>{label}</span>
        <b>
          {fmt(used)} / {limit ? fmt(limit) : '不限'}
        </b>
      </div>
      <i className="progressTrack">
        <em className={`progressFill ${limit ? tone : 'muted'}`} style={{width: `${limit ? pct : 0}%`}} />
      </i>
    </div>
  )
}
