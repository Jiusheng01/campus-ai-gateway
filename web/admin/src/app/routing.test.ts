import {describe, expect, it} from 'vitest'
import {landingPath, mayUseWorkspace, pathFor, resolveRoute} from './routing'

describe('workspace resolver', () => {
  it('maps both workspace roots to their default page', () => {
    expect(resolveRoute('/admin-ui')).toEqual({workspace: 'admin', page: 'dashboard'})
    expect(resolveRoute('/admin-ui/')).toEqual({workspace: 'admin', page: 'dashboard'})
    expect(resolveRoute('/console')).toEqual({workspace: 'console', page: 'overview'})
    expect(resolveRoute('/console/')).toEqual({workspace: 'console', page: 'overview'})
  })

  it('maps an addressed page under each prefix', () => {
    expect(resolveRoute('/console/keys')).toEqual({workspace: 'console', page: 'keys'})
    expect(resolveRoute('/admin-ui/users')).toEqual({workspace: 'admin', page: 'users'})
    expect(resolveRoute('/console/usage')).toEqual({workspace: 'console', page: 'usage'})
  })

  it('does not let one workspace resolve the other workspace page ids', () => {
    expect(resolveRoute('/admin-ui/overview')).toBeNull()
    expect(resolveRoute('/admin-ui/usage')).toBeNull()
    expect(resolveRoute('/console/providers')).toBeNull()
    expect(resolveRoute('/console/users')).toBeNull()
  })

  it('rejects paths that are not addressable pages', () => {
    expect(resolveRoute('/')).toBeNull()
    expect(resolveRoute('/foo')).toBeNull()
    expect(resolveRoute('/console/keys/extra')).toBeNull()
  })

  it('round-trips a page through its URL', () => {
    expect(pathFor('console', 'keys')).toBe('/console/keys')
    expect(pathFor('admin', 'users')).toBe('/admin-ui/users')
  })
})

describe('landing and workspace access', () => {
  it('sends administrators to the admin workspace and members to the console', () => {
    expect(landingPath(['super_admin'])).toBe('/admin-ui/dashboard')
    expect(landingPath(['gateway_admin'])).toBe('/admin-ui/dashboard')
    expect(landingPath(['member'])).toBe('/console/overview')
    expect(landingPath([])).toBe('/console/overview')
  })

  it('lets any account use the console but only management roles use the admin workspace', () => {
    expect(mayUseWorkspace('console', ['member'])).toBe(true)
    expect(mayUseWorkspace('console', [])).toBe(true)
    expect(mayUseWorkspace('admin', ['member'])).toBe(false)
    // The withdrawn global tenant_admin grant offers no admin workspace access.
    expect(mayUseWorkspace('admin', ['tenant_admin'])).toBe(false)
    expect(mayUseWorkspace('admin', ['auditor'])).toBe(true)
    expect(mayUseWorkspace('admin', ['super_admin'])).toBe(true)
  })
})
