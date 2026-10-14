import {useState, type FormEvent} from 'react'
import {AlertTriangle, Loader2, ShieldCheck} from 'lucide-react'
import {setupAdmin, type AuthConfig, type LoginResult} from '../api'
import {PasswordInput} from '../components/ui/PasswordInput'

export function SetupPage({config, onDone}: {config: AuthConfig | null; onDone: (result: LoginResult) => void}) {
  const [username, setUsername] = useState('admin')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const min = config?.min_password_length || 10
  const remoteBlocked = !!config?.setup_local_only && !config?.setup_allowed_here
  const mismatch = confirmPassword !== '' && password !== confirmPassword

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
      // Setup signs the operator in directly, so the token it returns decides
      // where they land — a super administrator on the dashboard.
      const result = await setupAdmin({
        username: username.trim(),
        display_name: displayName.trim(),
        password,
        confirm_password: confirmPassword,
      })
      onDone(result)
    } catch (err) {
      setError(err instanceof Error ? err.message : '初始化失败')
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
            <h1>创建管理员账号</h1>
            <p>首次启动 · 校园模型网关</p>
          </div>
        </div>

        {remoteBlocked ? (
          <div className="authError">
            <AlertTriangle aria-hidden />
            出于安全考虑，首次初始化只允许在服务器本机进行。请在服务器上打开{' '}
            <code>http://127.0.0.1:8080/admin-ui</code> 完成创建。
          </div>
        ) : (
          <div className="authNotice">
            <ShieldCheck aria-hidden />
            这是首次启动，请创建唯一的超级管理员账号。该账号拥有全部管理权限。
          </div>
        )}

        <label className="field">
          <span className="fieldLabel">用户名</span>
          <input
            autoFocus
            autoComplete="username"
            value={username}
            placeholder="用于日常登录，例如 admin"
            onChange={e => setUsername(e.target.value)}
          />
        </label>

        <label className="field">
          <span className="fieldLabel">显示名称（可选）</span>
          <input value={displayName} placeholder="例如 信息化中心管理员" onChange={e => setDisplayName(e.target.value)} />
        </label>

        <label className="field">
          <span className="fieldLabel">密码</span>
          <PasswordInput autoComplete="new-password" value={password} placeholder={`至少 ${min} 位`} onChange={setPassword} />
          <small className="fieldHint">
            至少 {min} 位，且需包含字母、数字、符号中的至少两类；不能与用户名相同。
          </small>
        </label>

        <label className="field">
          <span className="fieldLabel">确认密码</span>
          <PasswordInput
            autoComplete="new-password"
            value={confirmPassword}
            placeholder="请再次输入密码"
            onChange={setConfirmPassword}
          />
        </label>

        {mismatch && (
          <div className="authError">
            <AlertTriangle aria-hidden />
            两次输入的密码不一致
          </div>
        )}
        {error && (
          <div className="authError">
            <AlertTriangle aria-hidden />
            {error}
          </div>
        )}

        <button
          className="btn primary md authSubmit"
          type="submit"
          disabled={busy || remoteBlocked || !username.trim() || !password || password !== confirmPassword}
        >
          {busy ? (
            <>
              <Loader2 className="spin" aria-hidden />
              正在创建
            </>
          ) : (
            <>
              <ShieldCheck aria-hidden />
              创建并登录
            </>
          )}
        </button>
      </form>
    </div>
  )
}
