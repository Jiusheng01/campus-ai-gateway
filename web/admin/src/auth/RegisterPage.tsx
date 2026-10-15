import {useState, type FormEvent} from 'react'
import {AlertTriangle, Loader2, UserPlus} from 'lucide-react'
import {ApiError, register, type AuthConfig} from '../api'
import {PasswordInput} from '../components/ui/PasswordInput'
import {friendlyError} from '../lib/errors'

/**
 * Public sign-up.
 *
 * The body sent to the server carries four fields and nothing else: the gateway
 * decodes it with DisallowUnknownFields, so a request that also mentions roles
 * or status is refused rather than quietly accepted. There is deliberately no
 * "become an administrator" option here — an administrator is made by a super
 * administrator, in 用户与权限, and nowhere else.
 *
 * On success the server signs the new account in, so this page calls onDone
 * with the account's roles and the caller decides where that account belongs.
 */
export function RegisterPage({
  config,
  onDone,
  onSwitchToLogin,
  onAccountCreated,
}: {
  config: AuthConfig | null
  onDone: () => void
  onSwitchToLogin: () => void
  onAccountCreated: (username: string) => void
}) {
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const min = config?.min_password_length || 10

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (busy) return
    if (password !== confirmPassword) {
      setError('两次输入的密码不一致')
      return
    }
    setBusy(true)
    setError('')
    try {
      await register({username: username.trim(), display_name: displayName.trim(), password, confirm_password: confirmPassword})
      onDone()
    } catch (err) {
      // The account exists even though the automatic sign-in did not happen.
      // Retrying the registration would only be answered "username taken",
      // which told the old user nothing about the account they had just
      // successfully created — so the page drops the passwords and hands the
      // username to the login form. The next step is to sign in, not to
      // register again, and the client never re-sends the request by itself.
      if (err instanceof ApiError && err.code === 'account_created_login_failed') {
        const created = username.trim()
        setPassword('')
        setConfirmPassword('')
        onAccountCreated(created)
        return
      }
      setError(friendlyError(err, '创建账号失败，请稍后重试。'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="authForm" onSubmit={submit} noValidate>
      <label className="field">
        <span className="fieldLabel">用户名</span>
        <input
          autoFocus
          autoComplete="username"
          value={username}
          placeholder="用于登录的账号名"
          onChange={e => setUsername(e.target.value)}
        />
      </label>

      <label className="field">
        <span className="fieldLabel">显示名称（可选）</span>
        <input
          value={displayName}
          placeholder="例如 张三"
          autoComplete="nickname"
          onChange={e => setDisplayName(e.target.value)}
        />
      </label>

      <label className="field">
        <span className="fieldLabel">密码</span>
        <PasswordInput
          autoComplete="new-password"
          value={password}
          placeholder={`至少 ${min} 位`}
          onChange={setPassword}
        />
        <small className="fieldHint">
          至少 {min} 位，且需包含字母、数字、符号中的至少两类；不能与用户名相同。
        </small>
      </label>

      <label className="field">
        <span className="fieldLabel">确认密码</span>
        <PasswordInput autoComplete="new-password" value={confirmPassword} placeholder="请再次输入密码" onChange={setConfirmPassword} />
      </label>

      {error && (
        <div className="authError">
          <AlertTriangle aria-hidden />
          {error}
        </div>
      )}

      <button className="btn primary md authSubmit" type="submit" disabled={busy || !username.trim() || !password || !confirmPassword}>
        {busy ? (
          <>
            <Loader2 className="spin" aria-hidden />
            正在创建
          </>
        ) : (
          <>
            <UserPlus aria-hidden />
            创建账号
          </>
        )}
      </button>

      <p className="authHint">
        已有账号？
        <button type="button" className="linkInline" onClick={onSwitchToLogin}>
          返回登录
        </button>
      </p>
      <p className="authHint subtle">注册即表示你同意以真实身份使用校园模型网关；管理员可随时停用违规账号。</p>
    </form>
  )
}
