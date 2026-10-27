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
  type LoginResult,
  type WhoAmI,
} from '../api'
import {Shell} from './Shell'
import {allowedPages, type Page} from './navigation'
import {landingPath, mayUseWorkspace, pathFor, resolveRoute} from './routing'
import {ToastProvider, useToast} from '../components/ui/Toast'
import {ConfirmProvider} from '../components/ui/ConfirmDialog'
import {AuthSplash} from '../auth/AuthSplash'
import {AuthUnsupported} from '../auth/AuthUnsupported'
import {IdentityRequired} from '../auth/IdentityRequired'
import {LoginPage, PasswordResetRequiredPage} from '../auth/LoginPage'
import {SetupPage} from '../auth/SetupPage'
import {WorkspaceForbidden, WorkspaceNotFound} from '../auth/WorkspaceBoundary'
import {DashboardPage} from '../pages/DashboardPage'
import {AnalyticsPage} from '../pages/AnalyticsPage'
import {ProvidersPage} from '../pages/ProvidersPage'
import {PublishedModelsPage} from '../pages/PublishedModelsPage'
import {RoutesPage} from '../pages/RoutesPage'
import {CallsPage} from '../pages/CallsPage'
import {RequestLogsPage} from '../pages/RequestLogsPage'
import {AuditPage} from '../pages/AuditPage'
import {SecurityPage} from '../pages/SecurityPage'
import {UsersPage} from '../pages/UsersPage'
import {SettingsPage} from '../pages/SettingsPage'
import {ProfilePage} from '../pages/ProfilePage'
import {AccessPage} from '../pages/AccessPage'
import {ConsoleOverviewPage} from '../pages/ConsoleOverviewPage'
import {ConsoleKeysPage} from '../pages/ConsoleKeysPage'
import {ConsoleModelsPage} from '../pages/ConsoleModelsPage'
import {ConsoleLogsPage} from '../pages/ConsoleLogsPage'
import {ConsoleUsagePage} from '../pages/ConsoleUsagePage'

// loading   — checking the auth config and trying to restore a session
// setup     — no administrator exists yet, show the first-run form
// login     — credentials required (session mode)
// ready     — signed in
// identity  — trusted-header mode: the campus proxy must inject an identity
// unsupported — the gateway is running a legacy auth mode this UI cannot drive
//
// Registration is not a separate phase: it lives in a tab on the login screen,
// because a member and an administrator differ by role rather than by kind.
type AuthPhase = 'loading' | 'login' | 'setup' | 'ready' | 'identity' | 'unsupported'

type LogSeed = {model?: string; provider?: string; tenant_id?: string; api_key_id?: string}

const authModeOf = (cfg: AuthConfig | null) => (cfg?.auth_mode || '').toLowerCase()

const isWorkspacePath = (path: string) => path.startsWith('/admin-ui') || path.startsWith('/console')

function Console() {
  const toast = useToast()
  const [phase, setPhase] = useState<AuthPhase>('loading')
  const [authConfig, setAuthConfig] = useState<AuthConfig | null>(null)
  const [authNotice, setAuthNotice] = useState('')
  const [who, setWho] = useState<WhoAmI | null>(null)
  // The pathname is the routing state. A direct load, a refresh and a
  // back/forward gesture all arrive here through the URL rather than through
  // component state that a reload would lose.
  const [path, setPath] = useState(() => window.location.pathname)
  const [logSeed, setLogSeed] = useState<LogSeed>({})
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem('campus_sidebar') === '1')
  const [dark, setDark] = useState(() => localStorage.getItem('campus_theme') === 'dark')

  useEffect(() => {
    document.documentElement.dataset.theme = dark ? 'dark' : 'light'
    localStorage.setItem('campus_theme', dark ? 'dark' : 'light')
  }, [dark])

  // Back and forward move between workspaces and pages without a router. The
  // listener is the only place the URL writes back into component state.
  useEffect(() => {
    const onPopState = () => setPath(window.location.pathname)
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

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
      // A role change or a disable revokes the account's sessions; the operator
      // then sees why they were signed out instead of a silent bounce.
      setAuthNotice(
        reason === 'revoked'
          ? '会话已被撤销，请重新登录。如果刚有管理员调整了你的权限或账号状态，重新登录后即可看到变化。'
          : '登录状态已过期，请重新登录。',
      )
      setPhase('login')
    })
  }, [authConfig])

  // Derived, not state: the shell and every page read the same list, and it is
  // refreshed wholesale when whoami resolves.
  const roles = who?.roles || []
  const canOpenRawLogs = roles.includes('super_admin') || roles.includes('auditor')

  // If the account arrives at a path that names no workspace at all (most
  // commonly "/"), send it to the landing its roles choose. A path that names a
  // real workspace is left alone: a member asking for /admin-ui must see the
  // 403 page, not a redirect that hides what happened.
  useEffect(() => {
    if (phase !== 'ready' || !who) return
    if (resolveRoute(path)) return
    if (isWorkspacePath(path)) return
    const target = landingPath(who.roles || [])
    window.history.replaceState({}, '', target)
    setPath(target)
  }, [phase, who, path])

  // The public auth config is the answer to a question whose answer changes:
  // before the first super administrator exists, registration is closed, and it
  // opens the moment setup completes. The console fetches it once at boot, but
  // the login screen is re-entered many times — logout, a revoked session, a
  // role change — so serving the copy captured before setup would leave the
  // registration tab hidden for the rest of the browser session. Refresh it
  // whenever the login or setup screen is shown instead.
  //
  // Like every hook here this sits above the early returns; a hook reached on
  // some renders but not others changes the hook count, which React refuses to
  // render.
  useEffect(() => {
    if (phase !== 'login' && phase !== 'setup') return
    let cancelled = false
    void getAuthConfig()
      .then(cfg => {
        if (!cancelled) setAuthConfig(cfg)
      })
      .catch(() => {
        // Keep the previous config: the screen still renders, and a failure
        // here must not stop someone signing in.
      })
    return () => {
      cancelled = true
    }
  }, [phase])

  // The identity chip lives in the shell on every page, so it is resolved once
  // per sign-in rather than per page. The roles from the sign-in response seed
  // it so the navigation does not briefly default to "member only".
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

  // onAuthenticated is called by every path that ends with a session: setup,
  // login and registration. It deliberately does not choose a page: the URL the
  // account arrived on is honoured when it names a workspace, and the landing
  // redirect above handles the case where it does not.
  function onAuthenticated(result?: LoginResult) {
    const resultRoles = result?.user?.roles || []
    setAuthNotice('')
    // Toasts are cleared on every account change: one about another user's
    // permissions must never follow a member into their own session.
    toast.clear()
    setWho(
      result?.user
        ? {
            user: result.user.username,
            username: result.user.username,
            roles: resultRoles,
            auth_mode: authModeOf(authConfig) || 'session',
          }
        : null,
    )
    setPhase('ready')
  }

  async function signOut() {
    await logout()
    clearSession()
    setWho(null)
    setAuthNotice('已安全退出登录。')
    toast.clear()
    setPhase('login')
  }

  // Navigation pushes a real URL. The Shell is workspace-aware, so the same
  // page id (profile, access) stays inside the workspace the account is in.
  function navigate(next: Page) {
    const current = resolveRoute(path)
    if (!current) return
    const target = pathFor(current.workspace, next)
    if (window.location.pathname !== target) window.history.pushState({}, '', target)
    setPath(target)
  }

  function goHome() {
    const target = landingPath(roles)
    if (window.location.pathname !== target) window.history.replaceState({}, '', target)
    setPath(target)
  }

  const openLogs = (filters: LogSeed) => {
    setLogSeed(filters)
    navigate('logs')
  }

  if (phase === 'loading') return <AuthSplash text="正在检查登录状态…" />
  if (phase === 'unsupported') return <AuthUnsupported config={authConfig} />
  if (phase === 'identity') return <IdentityRequired onRetry={() => window.location.reload()} />
  if (phase === 'setup') return <SetupPage config={authConfig} onDone={onAuthenticated} />
  if (phase === 'login') return <LoginPage config={authConfig} notice={authNotice} onDone={onAuthenticated} />

  // The session is restored but whoami has not answered yet. Deciding the
  // workspace before the roles arrive is what used to bounce an administrator
  // to 个人资料; here it would briefly show a 403 to a legitimate member.
  if (!who) return <AuthSplash text="正在读取账号身份…" />

  // A reset password is one an administrator chose and may still hold. The
  // gateway refuses every page but the password form, so the app does not
  // render a workspace it knows will answer 403 — it asks for the new password
  // first. Signing out is still offered, and lands on the ordinary login page.
  if (who.password_reset_required) {
    return <PasswordResetRequiredPage onSignedOut={() => void signOut()} />
  }

  const route = resolveRoute(path)
  if (!route) {
    if (isWorkspacePath(path)) return <WorkspaceNotFound onHome={goHome} />
    return <AuthSplash text="正在打开工作台…" />
  }

  if (!mayUseWorkspace(route.workspace, roles)) {
    return (
      <WorkspaceForbidden
        workspace={route.workspace}
        onHome={goHome}
        onSignOut={() => void signOut()}
        sessionMode={isSessionMode()}
      />
    )
  }

  // The workspace is usable, but an administrator may still lack the role for
  // one particular page. Say so rather than silently rewriting the address.
  if (route.workspace === 'admin' && !allowedPages(roles).includes(route.page)) {
    return (
      <WorkspaceForbidden
        workspace={route.workspace}
        scope="page"
        onHome={goHome}
        onSignOut={() => void signOut()}
        sessionMode={isSessionMode()}
      />
    )
  }

  return (
    <Shell
      page={route.page}
      workspace={route.workspace}
      onNavigate={navigate}
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
      {route.workspace === 'console' ? (
        <>
          {route.page === 'overview' && <ConsoleOverviewPage onNavigate={navigate} />}
          {route.page === 'keys' && <ConsoleKeysPage />}
          {route.page === 'models' && <ConsoleModelsPage />}
          {route.page === 'usage' && <ConsoleUsagePage />}
          {route.page === 'logs' && <ConsoleLogsPage />}
          {route.page === 'profile' && <ProfilePage who={who} />}
          {route.page === 'access' && <AccessPage who={who} onSignedOut={() => void signOut()} minPasswordLength={authConfig?.min_password_length} />}
        </>
      ) : (
        <>
          {route.page === 'dashboard' && <DashboardPage roles={roles} onNavigate={navigate} />}
          {route.page === 'analytics' && <AnalyticsPage />}
          {route.page === 'providers' && (
            <ProvidersPage
              canPreviewContent={roles.includes('super_admin')}
              canOpenRawLogs={canOpenRawLogs}
              onOpenLogs={openLogs}
            />
          )}
          {route.page === 'models' && (
            <PublishedModelsPage
              canAuthorize={roles.includes('super_admin')}
              canOpenRawLogs={canOpenRawLogs}
              onOpenLogs={openLogs}
            />
          )}
          {route.page === 'routes' && <RoutesPage />}
          {(route.page === 'calls' || route.page === 'keys') && <CallsPage key="calls-keys" initialTab="keys" />}
          {route.page === 'tenants' && <CallsPage key="calls-tenants" initialTab="tenants" />}
          {route.page === 'logs' && <RequestLogsPage key={JSON.stringify(logSeed)} initialFilters={logSeed} />}
          {route.page === 'audit' && <AuditPage />}
          {route.page === 'security' && <SecurityPage />}
          {route.page === 'users' && <UsersPage />}
          {route.page === 'settings' && <SettingsPage who={who} authConfig={authConfig} />}
          {route.page === 'profile' && <ProfilePage who={who} />}
          {route.page === 'access' && <AccessPage who={who} onSignedOut={() => void signOut()} minPasswordLength={authConfig?.min_password_length} />}
        </>
      )}
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
