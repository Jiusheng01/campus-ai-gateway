import {ShieldCheck, UserRound} from 'lucide-react'
import {api, type WhoAmI} from '../api'
import {Panel} from '../components/ui/Panel'
import {ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {useResource} from '../lib/useResource'
import {primaryRole, roleLabel, roleSummary} from '../lib/roles'

/** How the account signed in, in the same words the settings page uses. */
function authModeLabel(mode?: string): string {
  switch ((mode || '').toLowerCase()) {
    case 'session':
      return '账号密码登录'
    case 'trusted-header':
      return '学校统一身份认证'
    case 'token':
      return '应急 Token 模式'
    default:
      return '未返回'
  }
}

/**
 * Who the account is, and what it may reach.
 *
 * This used to be one page with the password and device controls. Two things
 * were wrong with that: the identity block sat above a form most visits never
 * touch, and the page was member-only, so an administrator had no page that
 * simply said which roles they hold.
 */
export function ProfilePage({who: initialWho}: {who: WhoAmI | null}) {
  const {data, loading, error, errorStatus, reload} = useResource(() => api<WhoAmI>('/admin/whoami', {}, false))
  const who = data || initialWho

  const name = who ? who.username || who.user : ''
  const roles = who?.roles || []
  const isManager = roles.some(r => r !== 'member')

  return (
    <div className="stack">
      {error ? (
        <Panel title="个人资料">
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        </Panel>
      ) : loading && !who ? (
        <Panel title="个人资料">
          <SkeletonRows rows={3} columns={2} />
        </Panel>
      ) : (
        <Panel title="个人资料" sub="账号身份、角色与权限范围。">
          <div className="accountHero">
            <span className="accountAvatar" aria-hidden>
              <UserRound />
            </span>
            <div className="accountIdentity">
              <b className="accountName">{name || '正在读取账号身份…'}</b>
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
                {/* The identity endpoint reports how this account signed in,
                    not whether it is healthy. 正常 would be a status verdict
                    the response cannot support. */}
                <dt>登录方式</dt>
                <dd>{authModeLabel(who?.auth_mode)}</dd>
              </div>
              <div>
                <dt>权限摘要</dt>
                <dd className="muted">{roleSummary(roles)}</dd>
              </div>
            </dl>
          </div>
        </Panel>
      )}

      <Panel title="权限范围" sub="当前账号可访问的管理界面范围。">
        {isManager ? (
          <div className="panelPad">
            <p className="hint">
              以侧栏可见页面为准；页面内返回 403 表示该操作超出角色权限。
            </p>
          </div>
        ) : (
          <>
            <div className="accountNotice">
              <ShieldCheck aria-hidden />
              <div>
                <b>当前账号暂无网关管理权限。</b>
                <p>
                  如需管理渠道、模型线路、租户或 API 密钥，请联系超级管理员在「用户」中为你的账号分配角色。
                </p>
              </div>
            </div>
            <ul className="accountCan">
              <li>修改本账号密码</li>
              <li>查看和管理自己的登录设备</li>
              <li>查看账号状态与角色</li>
            </ul>
          </>
        )}
      </Panel>
    </div>
  )
}
