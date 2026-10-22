export type RouteDraft = {
  id?: string
  provider_id: string
  upstream_model: string
  priority: number
  weight: number
  timeout_ms: number
  status: number
}

export type RouteMode = 'advanced' | 'primary' | 'weighted'

export function normalWeight(weight: number): number {
  return weight <= 0 ? 1 : weight
}

/** Primary/backup is an ordered list: priority 1..N and equal weight. */
export function toPrimaryBackup(routes: RouteDraft[]): RouteDraft[] {
  return routes.map((r, i) => ({...r, priority: i + 1, weight: 100}))
}

/** Weighted is the same route array; priority denotes the group. */
export function toWeighted(routes: RouteDraft[]): RouteDraft[] {
  return routes.map(r => ({...r}))
}

export function projectMode(mode: RouteMode, routes: RouteDraft[]): RouteDraft[] {
  if (mode === 'primary') return toPrimaryBackup(routes)
  return routes.map(r => ({...r}))
}

function keyOf(r: RouteDraft): string {
  return r.id || `${r.provider_id}\u0000${r.upstream_model}`
}

/**
 * Exact per-route/per-field diff. It is used before a mode switch can change
 * priority, weight, timeout or status; a mode view alone never writes.
 */
export function diffRoutes(before: RouteDraft[], after: RouteDraft[]): string[] {
  const beforeByKey = new Map(before.map(r => [keyOf(r), r]))
  const afterByKey = new Map(after.map(r => [keyOf(r), r]))
  const out: string[] = []
  for (const [key, a] of afterByKey) {
    const b = beforeByKey.get(key)
    const label = a.id ? `线路 ${a.id.slice(0, 8)}` : `${a.provider_id.slice(0, 8)} / ${a.upstream_model}`
    if (!b) {
      out.push(`新增：${label}（优先级 ${a.priority}，权重 ${a.weight}，超时 ${a.timeout_ms} ms，状态 ${a.status}）`)
      continue
    }
    if (b.priority !== a.priority) out.push(`${label}：优先级 ${b.priority} → ${a.priority}`)
    if (b.weight !== a.weight) out.push(`${label}：权重 ${b.weight} → ${a.weight}`)
    if (b.timeout_ms !== a.timeout_ms) out.push(`${label}：超时 ${b.timeout_ms} → ${a.timeout_ms} ms`)
    if (b.status !== a.status) out.push(`${label}：状态 ${b.status} → ${a.status}`)
  }
  for (const [key, b] of beforeByKey) {
    if (!afterByKey.has(key)) {
      const label = b.id ? `线路 ${b.id.slice(0, 8)}` : `${b.provider_id.slice(0, 8)} / ${b.upstream_model}`
      out.push(`移除：${label}`)
    }
  }
  return out
}

export type WeightedShare = {
  priority: number
  total: number
  items: {route: RouteDraft; pct: number}[]
  /** Lines in this band that receive no traffic; they are out of the denominator. */
  disabledCount: number
}

/**
 * Target percentages within each priority band. They are configuration targets,
 * not success rates.
 *
 * Only enabled lines are in the denominator. A disabled line never starts a
 * request, so counting its weight would report a share that cannot happen —
 * and zero weight is not its equivalent: the data plane schedules weight 0 as
 * weight 1, while only status 2 is out of rotation.
 */
export function weightedShares(routes: RouteDraft[]): WeightedShare[] {
  const bands = new Map<number, RouteDraft[]>()
  for (const r of routes) {
    const list = bands.get(r.priority) || []
    list.push(r)
    bands.set(r.priority, list)
  }
  return [...bands.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([priority, list]) => {
      const enabled = list.filter(r => r.status === 1)
      const total = enabled.reduce((sum, r) => sum + normalWeight(r.weight), 0)
      return {
        priority,
        total,
        items: enabled.map(r => ({route: r, pct: total ? (normalWeight(r.weight) * 100) / total : 0})),
        disabledCount: list.length - enabled.length,
      }
    })
}

function draftLabel(r: RouteDraft): string {
  return r.id ? `线路 ${r.id.slice(0, 8)}` : `${r.provider_id.slice(0, 8)} / ${r.upstream_model}`
}

/**
 * The complete difference between the saved route set and the draft, in the
 * words the operator will read before the write.
 *
 * `diffRoutes` compares two drafts for a mode switch, where the row order is
 * the same and only the fields move. This one is the final check before a save,
 * so it also compares the identity a line is keyed by (channel plus upstream
 * model) and status. A line whose identity changed is a removal plus an
 * addition, because the server treats it that way too. Row order alone is not
 * a change: priority is what the data plane orders by.
 */
export function saveIntent(saved: RouteDraft[], draft: RouteDraft[]): string[] {
  const beforeByKey = new Map(saved.map(r => [keyOf(r), r]))
  const afterByKey = new Map(draft.map(r => [keyOf(r), r]))
  const out: string[] = []
  for (const [key, a] of afterByKey) {
    const b = beforeByKey.get(key)
    if (!b) {
      out.push(`新增：${draftLabel(a)}（优先级 ${a.priority}，权重 ${a.weight}，超时 ${a.timeout_ms} ms，${a.status === 1 ? '启用' : '停用'}）`)
      continue
    }
    if (b.provider_id !== a.provider_id || b.upstream_model !== a.upstream_model) {
      out.push(`改为：${draftLabel(b)} → ${a.provider_id.slice(0, 8)} / ${a.upstream_model}`)
    }
    if (b.priority !== a.priority) out.push(`${draftLabel(a)}：优先级 ${b.priority} → ${a.priority}`)
    if (b.weight !== a.weight) out.push(`${draftLabel(a)}：权重 ${b.weight} → ${a.weight}`)
    if (b.timeout_ms !== a.timeout_ms) out.push(`${draftLabel(a)}：超时 ${b.timeout_ms} → ${a.timeout_ms} ms`)
    if (b.status !== a.status) out.push(`${draftLabel(a)}：状态 ${b.status === 1 ? '启用' : '停用'} → ${a.status === 1 ? '启用' : '停用'}`)
  }
  for (const [key, b] of beforeByKey) {
    if (!afterByKey.has(key)) out.push(`移除：${draftLabel(b)}`)
  }
  return out
}
