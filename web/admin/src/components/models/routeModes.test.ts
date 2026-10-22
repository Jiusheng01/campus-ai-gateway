import {describe, expect, it} from 'vitest'
import {diffRoutes, saveIntent, toPrimaryBackup, weightedShares, type RouteDraft} from './routeModes'

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

  // A disabled line receives no traffic, so including it in the denominator
  // would report a share that cannot happen. Zero weight is not a stand-in for
  // "disabled": the data plane schedules it as weight 1, and only status 2 is
  // out of rotation.
  it('weighted shares count only enabled lines in a band', () => {
    const shares = weightedShares([
      {id: 'a', provider_id: 'p', upstream_model: 'a', priority: 1, weight: 3, timeout_ms: 1, status: 1},
      {id: 'b', provider_id: 'p', upstream_model: 'b', priority: 1, weight: 6, timeout_ms: 1, status: 2},
      {id: 'c', provider_id: 'p', upstream_model: 'c', priority: 1, weight: 0, timeout_ms: 1, status: 1},
    ])
    expect(shares).toHaveLength(1)
    expect(shares[0].items.map(i => [i.route.id, i.pct])).toEqual([
      ['a', 75],
      ['c', 25],
    ])
    expect(shares[0].disabledCount).toBe(1)
  })

  it('reports a band whose every line is disabled without inventing a share', () => {
    const shares = weightedShares([
      {id: 'a', provider_id: 'p', upstream_model: 'a', priority: 4, weight: 2, timeout_ms: 1, status: 2},
    ])
    expect(shares[0].items).toEqual([])
    expect(shares[0].disabledCount).toBe(1)
  })
})

describe('save intent', () => {
  const saved: RouteDraft[] = [
    {id: 'r1', provider_id: 'p1', upstream_model: 'u1', priority: 1, weight: 100, timeout_ms: 60000, status: 1},
    {id: 'r2', provider_id: 'p2', upstream_model: 'u2', priority: 2, weight: 100, timeout_ms: 30000, status: 1},
  ]

  it('is empty when the draft matches the saved set, including field order', () => {
    expect(saveIntent(saved, saved.map(r => ({...r})))).toEqual([])
  })

  // Everything the operator can edit is a change they should read back before
  // it is written: fields, status, membership and the provider/upstream pair
  // that identifies a line. A line that keeps its id and changes its binding is
  // an in-place edit, which is what the server does with it.
  it('lists field edits, status flips, binding changes and additions', () => {
    const draft: RouteDraft[] = [
      {...saved[0], weight: 60, timeout_ms: 12000},
      {...saved[1], status: 2, provider_id: 'p3', upstream_model: 'u9'},
      {provider_id: 'p4', upstream_model: 'u4', priority: 3, weight: 100, timeout_ms: 1000, status: 1},
    ]
    const lines = saveIntent(saved, draft)
    expect(lines).toEqual(expect.arrayContaining([
      expect.stringMatching(/权重 100 → 60/),
      expect.stringMatching(/超时 60000 → 12000/),
      expect.stringMatching(/状态 启用 → 停用/),
      expect.stringMatching(/改为：.*r2 → p3 \/ u9/),
      expect.stringMatching(/新增：p4 \/ u4/),
    ]))
  })

  it('names every removed line', () => {
    const lines = saveIntent(saved, [saved[0]])
    expect(lines).toEqual([expect.stringMatching(/移除：线路 r2/)])
  })

  it('names a brand-new line by its upstream binding, not an index', () => {
    const lines = saveIntent([], [{provider_id: 'abcdefgh-1', upstream_model: 'new-model', priority: 1, weight: 1, timeout_ms: 1, status: 1}])
    expect(lines).toHaveLength(1)
    expect(lines[0]).toMatch(/新增：abcdefgh \/ new-model/)
  })

  it('ignores a row reorder that writes no field', () => {
    // The save sends the array in the order shown, but priority is the fact the
    // data plane uses. Swapping two display rows without editing them is not a
    // configuration change.
    expect(saveIntent(saved, [saved[1], saved[0]])).toEqual([])
  })
})
