import {useState} from 'react'
import {Pencil, ShieldAlert, UserCheck, UserRound, UserX, Users as UsersIcon} from 'lucide-react'
import {listUsers, updateUser, type AccountChangeSet, type AccountUser, type UserPatch} from '../api'
import {Panel} from '../components/ui/Panel'
import {Button} from '../components/ui/Button'
import {Badge } from '../components/ui/Badge'
import {EmptyState, ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {Drawer} from '../components/ui/Drawer'
import {Table} from '../components/ui/Table'
import {Detail} from '../components/ui/Detail'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {useResource} from '../lib/useResource'
import {ASSIGNABLE_ROLES, canonicalRoles, roleLabel} from '../lib/roles'
import {ago, num, when} from '../lib/format'

/**
 * 用户.
 *
 * Super-admin only, and the gateway enforces that on its own: /admin/users is
 * absent from every other role's allow-list, so a gateway_admin who forges a
 * request gets a 403 rather than a page. Hiding the entry is a courtesy; the
 * check on the server is the boundary.
 */
export function UsersPage() {
  const toast = useToast()
  const confirm = useConfirm()
  const {data, loading, error, reload} = useResource(async () => {
    const out = await listUsers()
    return out
  })
  const [editing, setEditing] = useState<AccountUser | null>(null)

  const users = data?.data || []
  const stats = data?.stats

  return (
    <>
      <Panel
        title="用户"
        sub="管理注册用户和管理员角色。管理员角色只能由超级管理员授予。"
      >
        <div className="kpiGrid">
          <Stat label="总用户" value={stats?.total ?? 0} icon={<UsersIcon />} />
          <Stat label="普通用户" value={stats?.members ?? 0} icon={<UserRound />} />
          <Stat label="管理员" value={stats?.admins ?? 0} icon={<UserCheck />} />
          <Stat label="停用账号" value={stats?.disabled ?? 0} icon={<UserX />} tone={stats?.disabled ? 'danger' : undefined} />
        </div>
      </Panel>

      <Panel title="账号列表" sub="点击「管理」可调整角色或停用账号。">
        {loading && !data ? (
          <SkeletonRows rows={6} columns={7} />
        ) : error ? (
          <ErrorState message={error} onRetry={reload} />
        ) : users.length ? (
          <Table heads={['用户', '显示名称', '角色', '状态', '最后登录', '注册时间', '操作']} minWidth={1080}>
            {users.map(u => (
              <tr key={u.id}>
                <td>
                  <b className="mono">{u.username}</b>
                </td>
                <td>{u.display_name || <span className="muted">—</span>}</td>
                <td>
                  {(u.roles || []).length ? (
                    u.roles.map(r => (
                      <Badge key={r} tone={r === 'super_admin' ? 'danger' : r === 'member' ? 'neutral' : 'info'}>
                        {roleLabel(r)}
                      </Badge>
                    ))
                  ) : (
                    <Badge>{roleLabel('member')}</Badge>
                  )}
                </td>
                <td>
                  <span className={`badge status ${u.status === 1 ? 'success' : 'neutral'}`}>
                    <i className={`dot ${u.status === 1 ? 'on' : 'off'}`} aria-hidden />
                    {u.status === 1 ? '正常' : '已停用'}
                  </span>
                </td>
                <td>{ago(u.last_login_at)}</td>
                <td>{when(u.created_at)}</td>
                <td>
                  <button className="link" onClick={() => setEditing(u)}>
                    <Pencil aria-hidden />
                    管理
                  </button>
                </td>
              </tr>
            ))}
          </Table>
        ) : (
          <EmptyState icon={<UsersIcon />} title="暂无账号" description="开放注册后，注册的账号会出现在这里。" />
        )}
      </Panel>

      {editing && (
        <UserDrawer
          user={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            reload()
          }}
          onDenied={message => toast.error(message)}
        />
      )}
    </>
  )
}

// managementRolesOf is the assignable subset of an account's roles. "member" is
// not one of them: it is what an account is when it holds nothing else.
function managementRolesOf(user: AccountUser): string[] {
  return (user.roles || []).filter(r => r !== 'member' && ASSIGNABLE_ROLES.includes(r))
}

/**
 * changeSummary describes what a save actually did.
 *
 * It is built from the gateway's change set rather than from what the form
 * submitted, so editing a display name never reports that the account was
 * signed out — because it was not.
 */
function changeSummary(changes: AccountChangeSet, status: number): string {
  if (!changes.roles && !changes.status) return '用户信息已更新。'
  if (changes.roles && changes.status) {
    return '用户设置已更新；权限或状态变更已使该用户现有登录会话失效。'
  }
  if (changes.roles) return '权限已更新，该用户现有登录会话已失效。'
  return status === 1 ? '账号已启用，用户可以重新登录。' : '账号已停用，该用户现有登录会话已失效。'
}

function Stat({label, value, icon, tone}: {label: string; value: number; icon: React.ReactNode; tone?: 'danger'}) {
  return (
    <div className={`kpi ${tone || ''}`.trim()}>
      <span className="kpiLabel">
        {icon}
        {label}
      </span>
      <strong className="kpiValue">{num(value)}</strong>
    </div>
  )
}

/**
 * The account drawer.
 *
 * Role changes and disabling both sign the target out immediately — the
 * gateway bumps auth_version and revokes their sessions — so the confirm copy
 * says so rather than leaving the operator to discover it.
 */
function UserDrawer({
  user,
  onClose,
  onSaved,
  onDenied,
}: {
  user: AccountUser
  onClose: () => void
  onSaved: () => void
  onDenied: (message: string) => void
}) {
  const toast = useToast()
  const confirm = useConfirm()
  const [selected, setSelected] = useState<string[]>(
    (user.roles || []).filter(r => r !== 'member' && ASSIGNABLE_ROLES.includes(r)),
  )
  const [status, setStatus] = useState(user.status)
  const [displayName, setDisplayName] = useState(user.display_name || '')
  const [busy, setBusy] = useState(false)

  // Comparisons are over the canonical form, so reordering the same roles is
  // not mistaken for an edit — otherwise merely opening the drawer and saving
  // would look like a role change and sign the account out.
  const initialRoles = canonicalRoles(managementRolesOf(user))
  const rolesChanged = canonicalRoles(selected) !== initialRoles
  const statusChanged = status !== user.status
  const displayNameChanged = displayName.trim() !== (user.display_name || '')
  const dirty = rolesChanged || statusChanged || displayNameChanged

  const grantsSuperAdmin = selected.includes('super_admin')
  const disabling = status !== 1

  const save = async () => {
    if (!dirty) {
      onClose()
      return
    }

    if (grantsSuperAdmin && !(user.roles || []).includes('super_admin')) {
      const ok = await confirm({
        title: '授予超级管理员权限？',
        description: (
          <>
            <b>{user.username}</b> 将拥有完整系统控制权限，包括管理所有渠道、租户与其它管理员。
            此变更会立即撤销该账号的现有登录会话。
          </>
        ),
        confirmText: '授予超级管理员',
        danger: true,
      })
      if (!ok) return
    }

    if (disabling && user.status === 1) {
      const ok = await confirm({
        title: `停用 ${user.username}？`,
        description: '该账号将立即退出登录且无法再次登录，其现有会话全部失效。',
        confirmText: '停用账号',
        danger: true,
      })
      if (!ok) return
    }

    // Only the fields that actually changed are sent. The gateway leaves an
    // absent field alone, so omitting the untouched ones is exactly what keeps
    // a display-name edit from being treated as a role change — and from
    // signing the account out.
    //
    // An empty selection means the account is an ordinary member: the server
    // applies that rule itself, and the UI never sends "member" explicitly so
    // it cannot end up alongside a management role.
    const patch: UserPatch = {}
    if (rolesChanged) patch.roles = selected
    if (statusChanged) patch.status = status
    if (displayNameChanged) patch.display_name = displayName.trim()

    setBusy(true)
    try {
      const {changes} = await updateUser(user.id, patch)
      toast.success(changeSummary(changes, status))
      onSaved()
    } catch (err) {
      const code = (err as {code?: string})?.code
      if (code === 'last_super_admin') {
        onDenied('系统至少需要保留一个启用的超级管理员，变更未应用。')
      } else {
        onDenied(err instanceof Error ? err.message : '保存失败')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Drawer
      title="管理账号"
      subtitle={user.username}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" loading={busy} disabled={!dirty} onClick={() => void save()}>
            保存权限
          </Button>
        </>
      }
    >
      <div className="detailStack">
        <Detail
          title="基本信息"
          rows={[
            ['用户名', <span key="u" className="mono">{user.username}</span>],
            ['注册时间', when(user.created_at)],
            ['最后登录', ago(user.last_login_at)],
          ]}
        />

        <div className="form">
          <label className="field">
            <span className="fieldLabel">显示名称</span>
            <input value={displayName} placeholder="例如 张三" onChange={e => setDisplayName(e.target.value)} />
          </label>
        </div>

        <section className="detailBox">
          <h3>状态</h3>
          <div className="detailRow">
            <span className="detailKey">账号状态</span>
            <b className="detailValue">
              <select value={status} onChange={e => setStatus(+e.target.value)}>
                <option value={1}>启用</option>
                <option value={2}>停用</option>
              </select>
            </b>
          </div>
          <div className="detailRow">
            <span className="detailKey">效果</span>
            <b className="detailValue muted">
              {status === 1 ? '停用将立即撤销全部登录会话' : '启用后该账号可重新登录'}
            </b>
          </div>
        </section>

        <section className="detailBox">
          <h3>权限</h3>
          <div className="detailRow column">
            <span className="detailKey">管理角色</span>
            <div className="checks">
              {ASSIGNABLE_ROLES.map(role => (
                <label key={role}>
                  <input
                    type="checkbox"
                    checked={selected.includes(role)}
                    onChange={e => {
                      if (e.target.checked) setSelected([...selected, role])
                      else setSelected(selected.filter(r => r !== role))
                    }}
                  />
                  {roleLabel(role)}
                </label>
              ))}
            </div>
            <small className="fieldHint">不选择任何管理角色时，该账号即为普通用户。</small>
          </div>
        </section>

        {grantsSuperAdmin && (
          <div className="notice warning">
            <ShieldAlert aria-hidden />
            超级管理员拥有完整系统控制权限，且系统必须始终保留至少一个启用的超级管理员。
          </div>
        )}
      </div>
    </Drawer>
  )
}
