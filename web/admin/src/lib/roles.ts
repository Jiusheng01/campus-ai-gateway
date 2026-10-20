// Role vocabulary shared by the navigation, the topbar and the user management
// page. Keeping the labels in one place is what stops "普通用户" on the nav from
// turning into "member" in the drawer.
export const ROLE_LABELS: Record<string, string> = {
  member: '普通用户',
  auditor: '审计员',
  operator: '运维人员',
  tenant_admin: '租户管理员',
  gateway_admin: '网关管理员',
  super_admin: '超级管理员',
}

export const roleLabel = (role: string) => ROLE_LABELS[role] || role

/**
 * The roles a super administrator may grant, in display order. "member" is not
 * in the list because it is what an account is when it holds nothing else: a
 * checkbox row for it would be two ways of saying the same thing.
 *
 * "tenant_admin" is assignable again now that 0015 gives it a real scope. It is
 * not a global grant: the account is confined to the subtree chosen alongside
 * the role, and the console refuses to save the role without one.
 */
export const ASSIGNABLE_ROLES = ['auditor', 'operator', 'tenant_admin', 'gateway_admin', 'super_admin']

/**
 * isManagement reports whether a role grants control-plane access. "member" is
 * the account that holds nothing else; every other role, including the scoped
 * tenant_admin, reaches at least part of the control plane.
 */
export const isManagementRole = (role: string) => role !== 'member' && role in ROLE_LABELS

/** All six roles, most privileged first, for the identity tooltip. */
export const ALL_ROLES = ['super_admin', 'gateway_admin', 'tenant_admin', 'operator', 'auditor', 'member']

/** The single role shown next to the username. */
export function primaryRole(roles: string[] | undefined): string {
  if (!roles || !roles.length) return 'member'
  return ALL_ROLES.find(r => roles.includes(r)) || 'member'
}

export function roleSummary(roles: string[] | undefined): string {
  if (!roles || !roles.length) return roleLabel('member')
  return roles.map(roleLabel).join('、')
}

/** Reports whether the principal holds any of the given roles. */
export function hasAnyRole(roles: string[] | undefined, wanted: string[]): boolean {
  if (!roles || !roles.length) return wanted.includes('member')
  return roles.some(r => wanted.includes(r))
}

/**
 * A comparable form of a role list.
 *
 * The stored order carries no meaning, so comparing raw arrays or a plain join
 * would report a change when the same roles simply arrived in a different
 * order — and a role change is what signs an account out.
 */
export function canonicalRoles(roles: string[]): string {
  return [...roles].sort().join(',')
}
