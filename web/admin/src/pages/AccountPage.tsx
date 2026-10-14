import {ShieldCheck, UserRound} from 'lucide-react'
import {api, type WhoAmI} from '../api'
import {Panel} from '../components/ui/Panel'
import {ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {AccountSecurity} from '../auth/AccountSecurity'
import {useResource} from '../lib/useResource'
import {primaryRole, roleLabel, roleSummary} from '../lib/roles'

/**
 * The member home page.
 *
 * A newly registered account lands here rather than on the dashboard: it has no
 * management role, so the dashboard would either be empty or answer 403 on
 * every KPI. Saying so plainly, and pointing at the person who can grant the
 * role, is more useful than an empty screen.
 *
 * It reuses AccountSecurity rather than reimplementing password and device
 * management — those are the only things an ordinary account can do, and they
 * should look and behave exactly as they do for an administrator.
 */
export function AccountPage({who: initialWho, onSignedOut}: {who: WhoAmI | null; onSignedOut: () => void}) {
  const {data, loading, error, reload} = useResource(() => api<WhoAmI>('/admin/whoami', {}, false))
  const who = data || initialWho

  const name = who ? who.username || who.user : ''
  const roles = who?.roles || []

  return (
    <div className="stack">
      {error ? (
        <Panel title="我的账号">
          <ErrorState message={error} onRetry={reload} />
        </Panel>
      ) : loading && !who ? (
        <Panel title="我的账号">
          <SkeletonRows rows={3} columns={2} />
        </Panel>
      ) : (
        <Panel
          title="我的账号"
          sub="账号状态与安全设置。"
        >
          <div className="accountHero">
            <span className="accountAvatar" aria-hidden>
              <UserRound />
            </span>
            <div className="accountIdentity">
              <b className="accountName">{name || '未知用户'}</b>
              <span className="accountHandle">@{who?.username || who?.user || '—'}</span>
            </div>
            <dl className="accountFacts">
              <div>
                <dt>角色</dt>
                <dd>
                  <span className="badge info">{roleLabel(primaryRole(roles))}</span>
                  {roles.length > 1 && <span className="muted">共 {roles.length} 项</span>}
                </dd>
              </div>
              <div>
                <dt>账号状态</dt>
                <dd>
                  <span className="badge success">正常</span>
                </dd>
              </div>
              <div>
                <dt>权限摘要</dt>
                <dd className="muted">{roleSummary(roles)}</dd>
              </div>
            </dl>
          </div>
        </Panel>
      )}

      <Panel title="当前权限" sub="你的账号目前对网关管理界面的访问范围。">
        <div className="accountNotice">
          <ShieldCheck aria-hidden />
          <div>
            <b>当前账号暂无网关管理权限。</b>
            <p>
              如需管理供应商、模型线路、租户或 API Key，请联系超级管理员在「用户与权限」中为你的账号分配角色。
            </p>
          </div>
        </div>
        <ul className="accountCan">
          <li>修改本账号密码</li>
          <li>查看和管理自己的登录设备</li>
          <li>查看账号状态与角色</li>
        </ul>
      </Panel>

      {who && <AccountSecurity who={who} onSignedOut={onSignedOut} />}
    </div>
  )
}
