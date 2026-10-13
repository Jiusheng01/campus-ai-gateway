import type {AuthConfig} from '../api'

/**
 * Shown when the gateway runs a mode this console cannot drive.
 *
 * There is deliberately no token input: the console never stores a credential,
 * so offering a box to paste one in would contradict that promise.
 */
export function AuthUnsupported({config}: {config: AuthConfig | null}) {
  const mode = (config?.auth_mode || '').toLowerCase()
  const tokenMode = mode === 'token' || mode === ''
  return (
    <div className="authPage">
      <div className="authCard authCenter">
        <div className="brandMark lg" aria-hidden>
          AI
        </div>
        <h1 className="authTitle">{tokenMode ? '当前为应急 Token 模式' : '控制台登录未启用'}</h1>
        {tokenMode ? (
          <p className="authHint">
            网关正以应急 Token 模式运行，管理控制台不提供登录入口。请通过管理 API 使用
            <code>ADMIN_TOKEN</code> 访问，或将 <code>ADMIN_AUTH_MODE</code> 切换为 <code>session</code>{' '}
            后使用账号密码登录。
          </p>
        ) : (
          <p className="authHint">
            当前网关的认证模式为 <code>{config?.auth_mode || '未知'}</code>
            ，管理控制台无法驱动该模式。请联系校园信息化管理部门确认网关配置。
          </p>
        )}
      </div>
    </div>
  )
}
