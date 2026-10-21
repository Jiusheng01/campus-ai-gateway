import {fmt} from './format'

/**
 * The three states of a tenant limit.
 *
 * They are not interchangeable. Zero is a configured limit that is deliberately
 * not enforced (无限制); an absent field means nobody configured a limit
 * (未设置); anything else is a number the gateway actually enforces. Rendering
 * zero and absent with the same label would state a promise the gateway never
 * made, so the distinction lives here and every surface that shows a tenant
 * limit goes through it.
 */
export type QuotaState = 'set' | 'unlimited' | 'unset'

export function quotaState(limit: number | null | undefined): QuotaState {
  if (limit === undefined || limit === null) return 'unset'
  if (limit === 0) return 'unlimited'
  return 'set'
}

/**
 * Text for a limit, independent of how a surface presents it: the tenant table
 * and its cards wrap the value in state tags, while the API Key drawer's quota
 * preview shows the same strings inline.
 */
export function quotaText(limit: number | null | undefined): string {
  const state = quotaState(limit)
  if (state === 'unset') return '未设置'
  if (state === 'unlimited') return '无限制'
  return fmt(limit)
}

/**
 * Percent with a fixed shape, so two bands (or an error rate) can be compared
 * at a glance. One implementation because the administrator quota strip and
 * the personal usage page render the same number.
 */
export function formatPercent(value: number): string {
  return `${value.toFixed(2)}%`
}

/**
 * Traffic-light tone for a share that is approaching its limit. The class
 * names are the stylesheet's; the thresholds are shared so the two quota
 * surfaces cannot disagree about when 70% turns amber.
 */
export function quotaTone(value: number): string {
  return value >= 90 ? 'danger' : value >= 70 ? 'warning' : ''
}
