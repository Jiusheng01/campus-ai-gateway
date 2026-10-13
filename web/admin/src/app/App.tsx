import {useEffect, useState} from 'react'
import {
  clearSession,
  getAuthConfig,
  isSessionMode,
  logout,
  refreshSession,
  setAuthMode,
  setUnauthenticatedHandler,
  whoami,
  type AuthConfig,
  type WhoAmI,
} from '../api'
import {Shell} from './Shell'
import type {Page} from './navigation'
import {ToastProvider, useToast} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import {AuthSplash} from '../auth/AuthSplash'
import {AuthUnsupported} from '../auth/AuthUnsupported'
import {IdentityRequired} from '../auth/IdentityRequired'
import {LoginPage} from '../auth/LoginPage'
import {SetupPage} from '../auth/SetupPage'
import {DashboardPage} from '../pages/DashboardPage'
import {ProvidersPage} from '../pages/ProvidersPage'
import {ModelsPage} from '../pages/ModelsPage'
import {TenantsPage} from '../pages/TenantsPage'
import {ApiKeysPage} from '../pages/ApiKeysPage'
import {RequestLogsPage} from '../pages/RequestLogsPage'
import {AuditPage} from '../pages/AuditPage'
import {SecurityPage} from '../pages/SecurityPage'
import {SettingsPage} from '../pages/SettingsPage'

// loading   — checking the auth config and trying to restore a session
// setup     — no administrator exists yet, show the first-run form
// login     — credentials required (session mode)
// ready     — signed in
// identity  — trusted-header mode: the campus proxy must inject an identity
// unsupported — the gateway is running a legacy auth mode this UI cannot drive
type AuthPhase = 'loading' | 'login' | 'setup' | 'ready' | 'identity' | 'unsupported'

const authModeOf = (cfg: AuthConfig | null) => (cfg?.auth_mode || '').toLowerCase()

function Console() {
  const toast = useToast()
  const [phase, setPhase] = useState<AuthPhase>('loading')
  const [authConfig, setAuthConfig] = useState<AuthConfig | null>(null)
  const [authNotice, setAuthNotice] = useState('')
  const [who, setWho] = useState<WhoAmI | null>(null)
  const [page, setPage] = useState<Page>('dashboard')
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem('campus_sidebar') === '1')
  const [dark, setDark] = useState(() => localStorage.getItem('campus_theme') === 'dark')

  useEffect(() => {
    document.documentElement.dataset.theme = dark ? 'dark' : 'light'
    localStorage.setItem('campus_theme', dark ? 'dark' : 'light')
  }, [dark])

  // Boot sequence: read the public auth config, then resolve the identity
  // appropriate to the active mode. In session mode that means restoring from
  // the HttpOnly refresh cookie; in trusted-header mode the campus reverse proxy
  // has already injected the identity, so /admin/whoami is the only call needed.
  // The access token is never restored from storage because it is never written
  // there.
  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const cfg = await getAuthConfig()
        if (cancelled) return
        setAuthConfig(cfg)
        setAuthMode(authModeOf(cfg))
        const mode = authModeOf(cfg)

        if (mode === 'trusted-header') {
          // No login form and no local session: ask the gateway who we are.
          try {
            const w = await whoami()
            if (cancelled) return
            setWho(w)
            setPhase('ready')
          } catch {
            if (!cancelled) setPhase('identity')
          }
          return
        }

        if (!cfg.login_enabled) {
          setPhase('unsupported')
          return
        }
        if (cfg.needs_setup) {
          setPhase('setup')
          return
        }
        const restored = await refreshSession()
        if (cancelled) return
        setPhase(restored ? 'ready' : 'login')
      } catch {
        if (!cancelled) setPhase('login')
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  // A 401 that survives one refresh attempt means the session is gone. What that
  // looks like depends on the mode: session mode returns to the login screen,
  // trusted-header mode can only report that the campus identity is missing or
  // lacks a management role.
  useEffect(() => {
    setUnauthenticatedHandler(reason => {
      setWho(null)
      if (authModeOf(authConfig) === 'trusted-header') {
        setPhase('identity')
        return
      }
      setAuthNotice(reason === 'revoked' ? '会话已被撤销，请重新登录。' : '登录状态已过期，请重新登录。')
      setPhase('login')
    })
  }, [authConfig])

  // The identity chip lives in the shell on every page, so it is resolved once
  // per sign-in rather than per page.
  useEffect(() => {
    if (phase !== 'ready' || who) return
    let cancelled = false
    void whoami()
      .then(w => {
        if (!cancelled) setWho(w)
      })
      .catch(() => {
        /* the global 401 handler already deals with an unusable identity */
      })
    return () => {
      cancelled = true
    }
  }, [phase, who])

  function onAuthenticated() {
    setAuthNotice('')
    setWho(null)
    setPhase('ready')
    setPage('dashboard')
  }

  async function signOut() {
    await logout()
    clearSession()
    setWho(null)
    setAuthNotice('已安全退出登录。')
    setPhase('login')
  }

  if (phase === 'loading') return <AuthSplash text="正在检查登录状态…" />
  if (phase === 'unsupported') return <AuthUnsupported config={authConfig} />
  if (phase === 'identity') return <IdentityRequired onRetry={() => window.location.reload()} />
  if (phase === 'setup') return <SetupPage config={authConfig} onDone={onAuthenticated} />
  if (phase === 'login') return <LoginPage config={authConfig} notice={authNotice} onDone={onAuthenticated} />

  return (
    <Shell
      page={page}
      onNavigate={setPage}
      collapsed={collapsed}
      onToggleCollapse={() => {
        const next = !collapsed
        setCollapsed(next)
        localStorage.setItem('campus_sidebar', next ? '1' : '0')
      }}
      dark={dark}
      onToggleTheme={() => setDark(v => !v)}
      who={who}
      sessionMode={isSessionMode()}
      refreshing={false}
      onSignOut={() => void signOut().catch(e => toast.error(e instanceof Error ? e.message : '退出登录失败'))}
    >
      {page === 'dashboard' && <DashboardPage />}
      {page === 'providers' && <ProvidersPage />}
      {page === 'models' && <ModelsPage />}
      {page === 'tenants' && <TenantsPage />}
      {page === 'keys' && <ApiKeysPage />}
      {page === 'logs' && <RequestLogsPage />}
      {page === 'audit' && <AuditPage />}
      {page === 'security' && <SecurityPage />}
      {page === 'settings' && <SettingsPage who={who} authConfig={authConfig} onSignedOut={() => void signOut()} />}
    </Shell>
  )
}

export default function App() {
  return (
    <ToastProvider>
      <ConfirmProvider>
        <Console />
      </ConfirmProvider>
    </ToastProvider>
  )
}
