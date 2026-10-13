// Presentation helpers shared by every page.
//
// Nothing here talks to the network: keeping formatting in one place is what
// stops "12.3K" on the dashboard from disagreeing with "12345" in a table.

/** Compact number for headline figures: 12345 -> 1.2万 */
export function fmt(n: number | null | undefined): string {
  return new Intl.NumberFormat('zh-CN', {
    notation: (n ?? 0) >= 10000 ? 'compact' : 'standard',
    maximumFractionDigits: 1,
  }).format(n || 0)
}

/** Full-precision number for tables, where the exact value matters. */
export function num(n: number | null | undefined): string {
  return new Intl.NumberFormat('zh-CN').format(n || 0)
}

export function when(v?: string | null): string {
  return v ? new Date(v).toLocaleString('zh-CN') : '—'
}

/** Relative time for "last used" style columns. */
export function ago(v?: string | null): string {
  if (!v) return '从未使用'
  const diff = Date.now() - Date.parse(v)
  if (Number.isNaN(diff)) return '—'
  const min = Math.floor(diff / 60000)
  if (min < 1) return '刚刚'
  if (min < 60) return `${min} 分钟前`
  const hours = Math.floor(min / 60)
  if (hours < 24) return `${hours} 小时前`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days} 天前`
  return new Date(v).toLocaleDateString('zh-CN')
}

export function ms(n: number | null | undefined): string {
  const v = n || 0
  return v < 1000 ? `${Math.round(v)} ms` : `${(v / 1000).toFixed(1)} s`
}

export function ratio(part: number, whole: number): string {
  if (!whole) return '0%'
  return `${((part / whole) * 100).toFixed(2)}%`
}

/** Trim the middle of a long identifier so both ends stay readable. */
export function middle(v: string, head = 10, tail = 4): string {
  if (!v || v.length <= head + tail + 1) return v
  return `${v.slice(0, head)}…${v.slice(-tail)}`
}

export function shortId(v?: string | null): string {
  if (!v) return '—'
  return v.length > 18 ? `${v.slice(0, 14)}…` : v
}

/**
 * Telemetry fields are genuinely absent when ThinkWatch does not report them.
 * Zero and false are real values, so only undefined/null/'' mean "unknown" —
 * the UI must never invent a provider or upstream model.
 */
export function isUnknown(v: unknown): boolean {
  return v === undefined || v === null || v === ''
}

export function orUnknown(v: unknown): string {
  return isUnknown(v) ? '未知' : String(v)
}

/** Copy helper that degrades gracefully when the clipboard API is blocked. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}
