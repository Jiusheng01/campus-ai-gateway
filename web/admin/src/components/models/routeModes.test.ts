import {describe, expect, it} from 'vitest'
import {diffRoutes, toPrimaryBackup, weightedShares, type RouteDraft} from './routeModes'

const routes: RouteDraft[] = [
  {id: 'r1', provider_id: 'p1', upstream_model: 'u1', priority: 3, weight: 30, timeout_ms: 60000, status: 1},
  {id: 'r2', provider_id: 'p2', upstream_model: 'u2', priority: 7, weight: 0, timeout_ms: 30000, status: 2},
]

describe('route mode projections', () => {
  it('primary/backup rewrites priority and weight but preserves timeout and status', () => {
    const projected = toPrimaryBackup(routes)
    expect(projected.map(r => [r.priority, r.weight, r.timeout_ms, r.status])).toEqual([
      [1, 100, 60000, 1],
      [2, 100, 30000, 2],
    ])
  })

  it('diff is exact and empty when nothing changes', () => {
    expect(diffRoutes(routes, routes.map(r => ({...r})))).toEqual([])
    const diff = diffRoutes(routes, toPrimaryBackup(routes))
    expect(diff).toEqual(expect.arrayContaining([
      expect.stringMatching(/优先级 3 → 1/),
      expect.stringMatching(/权重 30 → 100/),
      expect.stringMatching(/优先级 7 → 2/),
      expect.stringMatching(/权重 0 → 100/),
    ]))
  })

  it('weighted shares use normalWeight and are per priority band', () => {
    const shares = weightedShares([
      {id: 'a', provider_id: 'p', upstream_model: 'a', priority: 1, weight: 3, timeout_ms: 1, status: 1},
      {id: 'b', provider_id: 'p', upstream_model: 'b', priority: 1, weight: 1, timeout_ms: 1, status: 1},
      {id: 'c', provider_id: 'p', upstream_model: 'c', priority: 2, weight: 0, timeout_ms: 1, status: 1},
    ])
    expect(shares[0].items.map(i => i.pct)).toEqual([75, 25])
    expect(shares[1].items[0].pct).toBe(100)
  })
})
