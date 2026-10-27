import {useState, type FormEvent} from 'react'
import {AlertTriangle, Loader2, Lock, ShieldCheck} from 'lucide-react'
import {changePassword, clearSession, login, type AuthConfig, type LoginResult} from '../api'
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
/**
 * The screen an account sees after an administrator reset its password.
 *
 * It is not the ordinary change-password form because the situation is not
 * ordinary: the current password is one somebody else chose and may still have
 * written down, and the account cannot reach any page until it is replaced. The
 * copy says that plainly, and there is deliberately no "skip" — the gateway
 * would refuse the next request anyway.
 */
export function PasswordResetRequiredPage({onSignedOut}: {onSignedOut: () => void}) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirmNext, setConfirmNext] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (busy) return
    if (next !== confirmNext) {
      setError('两次输入的新密码不一致')
      return
    }
    setBusy(true)
    setError('')
    try {
      await changePassword(current, next)
      // The password change revokes every session, including this one, so the
      // only honest next step is the sign-in screen with the new password.
      clearSession()
      onSignedOut()
    } catch (err) {
      setError(friendlyError(err, '密码修改失败，请稍后重试。'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="authPage">
      <form className="authCard" onSubmit={submit}>
        <div className="authHead">
          <div className="brandMark lg" aria-hidden>
            AI
          </div>
          <div>
            <h1>设置你的新密码</h1>
            <p>管理员已重置此账号的密码</p>
          </div>
        </div>

        <div className="authNotice">
          <ShieldCheck aria-hidden />
          当前密码是管理员交给你的临时密码。请设置一个只有你自己知道的新密码；在此之前，此账号无法使用任何功能。
        </div>

        <label className="field">
          <span className="fieldLabel">临时密码</span>
          <PasswordInput autoComplete="current-password" value={current} placeholder="管理员交给你的密码" onChange={setCurrent} />
        </label>
        <label className="field">
          <span className="fieldLabel">新密码</span>
          <PasswordInput autoComplete="new-password" value={next} placeholder="至少 10 位" onChange={setNext} />
          <small className="fieldHint">至少 10 位，且需包含字母、数字、符号中的至少两类；不能与用户名相同。</small>
        </label>
        <label className="field">
          <span className="fieldLabel">确认新密码</span>
          <PasswordInput autoComplete="new-password" value={confirmNext} placeholder="请再次输入新密码" onChange={setConfirmNext} />
        </label>

        {error && (
          <div className="authError">
            <AlertTriangle aria-hidden />
            {error}
          </div>
        )}

        <button
          className="btn primary md authSubmit"
          type="submit"
          disabled={busy || !current || !next || next !== confirmNext}
        >
          {busy ? (
            <>
              <Loader2 className="spin" aria-hidden />
              正在保存
            </>
          ) : (
            <>
              <ShieldCheck aria-hidden />
              设置新密码
            </>
          )}
        </button>
        <button type="button" className="linkInline authSignOut" onClick={onSignedOut}>
          退出登录
        </button>
      </form>
    </div>
  )
}

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
