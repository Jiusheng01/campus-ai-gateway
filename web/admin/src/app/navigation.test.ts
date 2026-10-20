import {describe, expect, it} from 'vitest'
import {allowedPages, navGroups} from './navigation'

describe('model-service navigation', () => {
  it('offers the six model-service entries to a super administrator', () => {
    const modelService = navGroups.find(g => g.id === 'model-service')
    expect(modelService?.items.map(i => i.id)).toEqual([
      'dashboard',
      'providers',
      'models',
      'routes',
      'calls',
      'logs',
    ])
    expect(allowedPages(['super_admin'])).toEqual(
      expect.arrayContaining(['dashboard', 'providers', 'models', 'routes', 'calls', 'logs']),
    )
  })

  it('does not offer raw logs or key management to a gateway administrator', () => {
    const pages = allowedPages(['gateway_admin'])
    expect(pages).toEqual(expect.arrayContaining(['providers', 'models', 'routes']))
    expect(pages).not.toContain('logs')
    expect(pages).not.toContain('calls')
  })

  it('keeps the legacy keys/tenants page ids reachable for super_admin', () => {
    const pages = allowedPages(['super_admin'])
    expect(pages).toContain('keys')
    expect(pages).toContain('tenants')
  })
})
