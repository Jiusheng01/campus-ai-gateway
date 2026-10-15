import {useCallback, useEffect, useState, type FormEvent} from 'react'
import {AlertTriangle, KeyRound, Loader2, LogOut, MonitorSmartphone} from 'lucide-react'
import {changePassword, clearSession, listSessions, revokeSession, type AdminSessionView, type WhoAmI} from '../api'
import {Button} from '../components/ui/Button'
import {Panel} from '../components/ui/Panel'
import {Table} from '../components/ui/Table'
import {EmptyState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {PasswordInput} from '../components/ui/PasswordInput'
import {Field} from '../components/ui/Field'
import {Badge} from '../components/ui/Badge'
import {useConfirm} from '../components/ui/ConfirmDialog'
import {useToast} from '../components/ui/Toast'
import {when} from '../lib/format'

/**
 * 账号与安全 section.
 *
 * Session mode owns a password and a device list; trusted-header mode owns
 * neither, because the campus identity platform holds the credential. Showing
 * those forms there would imply this console stores something it does not.
 */
export function AccountSecurity({who, onSignedOut}: {who: WhoAmI | null; onSignedOut: () => void}) {
  const toast = useToast()
  const confirm = useConfirm()
  const sessionMode = (who?.auth_mode || '').toLowerCase() === 'session'

  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirmNext, setConfirmNext] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [sessions, setSessions] = useState<AdminSessionView[]>([])
  const [loadingSessions, setLoadingSessions] = useState(sessionMode)

  const reloadSessions = useCallback(async () => {
    setLoadingSessions(true)
    try {
      setSessions(await listSessions())
    } catch {
      /* the global 401 handler deals with an expired session */
    } finally {
      setLoadingSessions(false)
    }
  }, [])

  useEffect(() => {
    if (sessionMode) void reloadSessions()
  }, [sessionMode, reloadSessions])

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
      const out = await changePassword(current, next)
      toast.success(out?.message || '密码已更新')
      setCurrent('')
      setNext('')
      setConfirmNext('')
      // Every session was revoked by the change, so return to the login screen.
      clearSession()
      onSignedOut()
    } catch (err) {
      setError(err instanceof Error ? err.message : '修改密码失败')
    } finally {
      setBusy(false)
    }
  }

  const revoke = async (id: string) => {
    const ok = await confirm({
      title: '撤销该设备的登录状态？',
      description: '该设备需要重新登录才能继续使用管理控制台。',
      confirmText: '撤销设备',
      danger: true,
    })
    if (!ok) return
    try {
      await revokeSession(id)
      if (sessions.find(s => s.id === id)?.current) {
        toast.info('当前会话已撤销，请重新登录')
        onSignedOut()
        return
      }
      toast.success('已撤销该设备的登录状态')
      await reloadSessions()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : '撤销失败')
    }
  }

  const identityRows: [string, string][] = [
    ['用户名', who?.username || who?.user || '—'],
    ['角色', who?.roles?.length ? who.roles.join('、') : '—'],
    ['登录方式', sessionMode ? '账号密码登录' : '学校统一身份认证'],
  ]

  return (
    <>
      <Panel title="当前管理员" sub="权限由账号角色决定，角色变更即时生效。">
        <div className="kv">
          {identityRows.map(([k, v]) => (
            <div className="kvRow" key={k}>
              <span>{k}</span>
              <b>{v}</b>
            </div>
          ))}
          {sessionMode && (
            <div className="kvRow">
              <span>登录状态</span>
              <b>安全会话 · 自动续期</b>
            </div>
          )}
        </div>
        {sessionMode && (
          <div className="panelFoot">
            <Button icon={<LogOut />} onClick={onSignedOut}>
              退出登录
            </Button>
          </div>
        )}
      </Panel>

      {!sessionMode && (
        <Panel title="账号与安全" sub="密码与登录设备由校园统一身份认证平台管理。">
          <div className="panelPad">
            <p className="authHint">
              本控制台不保存账号密码。如需修改密码或注销其他设备，请前往校园统一身份认证平台操作。
            </p>
          </div>
        </Panel>
      )}

      {sessionMode && (
        <Panel title="修改密码" sub="修改成功后，所有设备上的登录状态都会立即失效，需要用新密码重新登录。">
          <form className="form" onSubmit={submit}>
            <Field label="当前密码">
              <PasswordInput autoComplete="current-password" value={current} onChange={setCurrent} />
            </Field>
            <Field label="新密码" hint="至少 10 位，且需包含字母、数字、符号中的至少两类。">
              <PasswordInput autoComplete="new-password" value={next} onChange={setNext} />
            </Field>
            <Field label="确认新密码">
              <PasswordInput autoComplete="new-password" value={confirmNext} onChange={setConfirmNext} />
            </Field>
            {error && (
              <div className="authError">
                <AlertTriangle aria-hidden />
                {error}
              </div>
            )}
            <div className="formActions">
              <Button
                variant="primary"
                type="submit"
                loading={busy}
                disabled={!current || !next || !confirmNext}
                icon={<KeyRound />}
              >
                更新密码
              </Button>
            </div>
          </form>
        </Panel>
      )}

      {sessionMode && (
        <Panel
          title="登录设备"
          sub="当前账号仍然有效的登录会话。撤销后该设备需要重新登录。"
          actions={
            <Button size="sm" icon={<MonitorSmartphone />} onClick={() => void reloadSessions()}>
              刷新
            </Button>
          }
        >
          {loadingSessions ? (
            <SkeletonRows rows={2} columns={4} />
          ) : sessions.length ? (
            <Table heads={['设备 / 来源', '登录方式', '最近活动', '到期时间', '操作']} minWidth={720}>
              {sessions.map(s => (
                <tr key={s.id}>
                  <td>
                    <b>{s.ip || '未知地址'}</b>
                    {s.current && <Badge tone="success">当前设备</Badge>}
                    <br />
                    <span className="muted cut" title={s.user_agent || ''}>
                      {s.user_agent || '未知客户端'}
                    </span>
                  </td>
                  <td>
                    <Badge>{s.login_method === 'password' ? '密码登录' : s.login_method}</Badge>
                  </td>
                  <td>{when(s.last_active_at)}</td>
                  <td>{when(s.expires_at)}</td>
                  <td>
                    <button className="link dangerText" onClick={() => void revoke(s.id)}>
                      撤销
                    </button>
                  </td>
                </tr>
              ))}
            </Table>
          ) : (
            <EmptyState title="没有其他有效登录会话" description="当前只有本设备的登录记录。" />
          )}
        </Panel>
      )}
    </>
  )
}
