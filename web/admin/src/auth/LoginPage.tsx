import {useState, type FormEvent} from 'react'
import {AlertTriangle, Loader2, Lock, ShieldCheck} from 'lucide-react'
import {login, type AuthConfig} from '../api'
import {PasswordInput} from '../components/ui/PasswordInput'

export function LoginPage({
  config,
  notice,
  onDone,
}: {
  config: AuthConfig | null
  notice: string
  onDone: () => void
}) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await login(username.trim(), password)
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
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
            <h1>校园模型网关</h1>
            <p>Campus AI Gateway 管理控制台</p>
          </div>
        </div>

        {notice && (
          <div className="authNotice">
            <ShieldCheck aria-hidden />
            {notice}
          </div>
        )}

        <label className="field">
          <span className="fieldLabel">用户名</span>
          <input
            autoFocus
            autoComplete="username"
            value={username}
            placeholder="请输入管理员用户名"
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

        <p className="authHint">忘记密码请联系校园信息化管理部门重置管理员账号。</p>
        {config && config.access_ttl_seconds > 0 && (
          <p className="authHint subtle">登录状态会安全保存并在后台自动续期，不会写入浏览器本地存储。</p>
        )}
      </form>
    </div>
  )
}
