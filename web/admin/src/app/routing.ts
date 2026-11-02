import {isManagementRole} from '../lib/roles'
import {
  adminRoutePages,
  allowedPages,
  consoleRoutePages,
  type Page,
} from './navigation'

/**
 * The two workspaces are one build addressed by path. There is no router
 * dependency: the pathname is the whole of the state, which is what makes a
 * direct load, a refresh and a back/forward gesture all resolve the same page.
 */
export type Workspace = 'admin' | 'console'

export type WorkspaceRoute = {workspace: Workspace; page: Page}

const ADMIN_PREFIX = '/admin-ui'
const CONSOLE_PREFIX = '/console'

/**
 * resolveRoute maps a pathname to a workspace and page.
 *
 * `/admin-ui` and `/console` are the workspace roots; their defaults are
 * `dashboard` and `overview`. Anything that is not an addressable page is null
 * rather than a best guess, so the caller can show an honest not-found instead
 * of silently rendering some other page.
 */
export function resolveRoute(pathname: string): WorkspaceRoute | null {
  const clean = pathname.replace(/\/+$/, '') || '/'

  if (clean === ADMIN_PREFIX) return {workspace: 'admin', page: 'dashboard'}
  if (clean.startsWith(ADMIN_PREFIX + '/')) {
    const page = clean.slice(ADMIN_PREFIX.length + 1) as Page
    return adminRoutePages.includes(page) ? {workspace: 'admin', page} : null
  }

  if (clean === CONSOLE_PREFIX) return {workspace: 'console', page: 'overview'}
  if (clean.startsWith(CONSOLE_PREFIX + '/')) {
    const page = clean.slice(CONSOLE_PREFIX.length + 1) as Page
    return consoleRoutePages.includes(page) ? {workspace: 'console', page} : null
  }

  return null
}

/** pathFor is the inverse of resolveRoute for a page the workspace owns. */
export function pathFor(workspace: Workspace, page: Page): string {
  return `${workspace === 'admin' ? ADMIN_PREFIX : CONSOLE_PREFIX}/${page}`
}

/**
 * mayUseWorkspace reports whether an account with these roles may open the
 * workspace. Any signed-in account may use the personal console; the
 * administrator workspace requires a management role. A tenant_admin counts:
 * its pages are narrowed to the subtree rather than withheld entirely.
 */
export function mayUseWorkspace(workspace: Workspace, roles: string[]): boolean {
  if (workspace === 'console') return true
  return roles.some(isManagementRole)
}

/** Where an account lands when no workspace was addressed. */
export function landingWorkspace(roles: string[]): Workspace {
  return roles.some(isManagementRole) ? 'admin' : 'console'
}

/**
 * The landing URL after login, used when the current path names no workspace.
 *
 * The dashboard is preferred, but a scoped tenant_admin cannot open it — its
 * numbers are campus-wide — so that account lands on the first page it may
 * actually enter instead of a 403.
 */
export function landingPath(roles: string[]): string {
  const workspace = landingWorkspace(roles)
  if (workspace === 'console') return pathFor('console', 'overview')
  const pages = allowedPages(roles)
  return pathFor('admin', pages.includes('dashboard') ? 'dashboard' : pages[0] || 'profile')
}
