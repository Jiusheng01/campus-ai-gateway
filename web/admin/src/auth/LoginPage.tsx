import {useState, type FormEvent} from 'react'
import {AlertTriangle, Loader2, Lock, ShieldCheck} from 'lucide-react'
import {login, type AuthConfig, type LoginResult} from '../api'
import {PasswordInput} from '../components/ui/PasswordInput'
import {RegisterPage} from './RegisterPage'
import {friendlyError} from '../lib/errors'

/**
 * The sign-in screen.
 *
 * It is a unified account entrance, not an administrator entrance: registration
 * lives in the same card behind a second tab, because an ordinary campus user
 * and an administrator are the same kind of account differing only by role.
 * The password placeholder says "账号" rather than "管理员用户名" for the same
 * reason.
 */
export function LoginPage({
  config,
  notice,
  onDone,
}: {
  config: AuthConfig | null
  notice: string
  onDone: (result?: LoginResult) => void
}) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [tab, setTab] = useState<'login' | 'register'>('login')
  // Set when a registration created the account but could not sign it in: the
  // login form is prefilled with the username and this explains why it is.
  const [banner, setBanner] = useState('')

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    setBanner('')
    try {
      const result = await login(username.trim(), password)
      onDone(result)
    } catch (err) {
      setError(friendlyError(err, '登录失败，请稍后重试。'))
    } finally {
      setBusy(false)
    }
  }

  // A registration that created the account but failed to sign it in hands the
  // username over here: the form is prefilled, the register tab is left, and
  // the passwords were already dropped by the register page.
  const accountCreated = (username: string) => {
    setUsername(username)
    setPassword('')
    setError('')
    setBanner('账号已创建成功，请使用刚设置的密码登录。')
    setTab('login')
  }

  return (
    <div className="authPage">
      <div className="authCard">
        <div className="authHead">
          <div className="brandMark lg" aria-hidden>
            AI
          </div>
          <div>
            <h1>校园模型网关</h1>
            <p>Campus AI Gateway 管理控制台</p>
          </div>
        </div>

        {(notice || banner) && (
          <div className="authNotice">
            <ShieldCheck aria-hidden />
            {notice || banner}
          </div>
        )}

        {/* Registration is offered only when this deployment accepts it, and
            never while the system still needs its first administrator: setup,
            not sign-up, creates that account. */}
        {config?.registration_enabled && (
          <div className="authTabs" role="tablist" aria-label="登录或注册">
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'login'}
              className={`authTab ${tab === 'login' ? 'active' : ''}`}
              onClick={() => setTab('login')}
            >
              登录
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'register'}
              className={`authTab ${tab === 'register' ? 'active' : ''}`}
              onClick={() => setTab('register')}
            >
              注册
            </button>
          </div>
        )}

        {tab === 'register' ? (
          <RegisterPage
            config={config}
            onDone={onDone}
            onSwitchToLogin={() => {
              setTab('login')
              setBanner('')
            }}
            onAccountCreated={accountCreated}
          />
        ) : (
          <form className="authForm" onSubmit={submit}>
            <label className="field">
              <span className="fieldLabel">用户名</span>
              <input
                autoFocus
                autoComplete="username"
                value={username}
                placeholder="请输入用户名"
                onChange={e => setUsername(e.target.value)}
              />
            </label>

            <label className="field">
              <span className="fieldLabel">密码</span>
              <PasswordInput autoComplete="current-password" value={password} placeholder="请输入密码" onChange={setPassword} />
            </label>

            {error && (
              <div className="authError">
                <AlertTriangle aria-hidden />
                {error}
              </div>
            )}

            <button className="btn primary md authSubmit" type="submit" disabled={busy || !username.trim() || !password}>
              {busy ? (
                <>
                  <Loader2 className="spin" aria-hidden />
                  正在登录
                </>
              ) : (
                <>
                  <Lock aria-hidden />
                  登录
                </>
              )}
            </button>

            {config?.registration_enabled ? (
              <p className="authHint">
                还没有账号？
                <button type="button" className="linkInline" onClick={() => setTab('register')}>
                  立即注册
                </button>
              </p>
            ) : (
              <p className="authHint">忘记密码请联系校园信息化管理部门重置账号。</p>
            )}
            {config && config.access_ttl_seconds > 0 && (
              <p className="authHint subtle">登录状态会安全保存并在后台自动续期，不会写入浏览器本地存储。</p>
            )}
          </form>
        )}
      </div>
    </div>
  )
}
