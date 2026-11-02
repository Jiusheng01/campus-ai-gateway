import {useCallback, useEffect, useState} from 'react'
import {Fingerprint, KeyRound, Lock, ShieldCheck} from 'lucide-react'
import {getAuthConfig, type AuthConfig, type WhoAmI} from '../api'
import {Panel} from '../components/ui/Panel'
import {Badge} from '../components/ui/Badge'
import {Button} from '../components/ui/Button'
import {EmptyState} from '../components/ui/EmptyState'
import {InfoDisclosure} from '../components/ui/InfoDisclosure'

type ModeInfo = {label: string; description: string; icon: typeof KeyRound; tone: 'success' | 'info' | 'warning'}

const MODE_INFO: Record<string, ModeInfo> = {
  session: {
    label: '账号密码登录',
    description: '使用网关自己的账号体系登录。访问凭证只保存在页面内存中，续期走 HttpOnly Cookie，不写入浏览器本地存储。',
    icon: KeyRound,
    tone: 'success',
  },
  'trusted-header': {
    label: '学校统一身份认证',
    description: '身份由校园反向代理注入，本控制台不保存账号密码，也不提供本地会话设备管理。',
    icon: Fingerprint,
    tone: 'info',
  },
  token: {
    label: '应急 Token 模式',
    description: '网关以应急 Token 运行，管理控制台不提供登录入口。该模式仅用于账号系统不可用时的应急访问。',
    icon: ShieldCheck,
    tone: 'warning',
  },
}

function modeInfo(mode: string): {info: ModeInfo; known: boolean} {
  if (MODE_INFO[mode]) return {info: MODE_INFO[mode], known: true}
  return {
    info: {
      label: '未识别的认证方式',
      description: '当前认证模式未在本控制台的已知模式列表中，无法确认它的行为。请联系校园信息化管理部门核对网关配置。',
      icon: ShieldCheck,
      tone: 'warning',
    },
    known: false,
  }
}

/** "15 分钟" for a whole number of minutes, "45 秒" otherwise. Never a rounded hour. */
function duration(seconds: number): string {
  if (!seconds || seconds < 0) return '—'
  if (seconds % 60 === 0) return `${seconds / 60} 分钟`
  return `${seconds} 秒`
}

function Fact({label, value}: {label: string; value: React.ReactNode}) {
  return (
    <div className="settingFact">
      <span className="settingFactLabel">{label}</span>
      <b className="settingFactValue">{value}</b>
    </div>
  )
}

/**
 * System settings describe the deployment, not the person using it.
 *
 * Everything here is read-only: the values come from the gateway's start-up
 * configuration, and no endpoint writes them back. The page therefore shows
 * them and says where they come from, rather than offering switches that could
 * not be saved. Password and device management live in 安全与访问: they are the
 * account's own credentials.
 */
export function SettingsPage({who, authConfig}: {who: WhoAmI | null; authConfig: AuthConfig | null}) {
  // The shell already read the config at boot; this is the fallback for a page
  // opened without it (a failed boot read, a direct test render) and the retry
  // path when that read failed.
  const [config, setConfig] = useState<AuthConfig | null>(authConfig)
  const [loadError, setLoadError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setConfig(authConfig)
  }, [authConfig])

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError('')
    try {
      setConfig(await getAuthConfig())
    } catch (e) {
      setConfig(null)
      setLoadError(e instanceof Error ? e.message : '无法读取认证配置')
    } finally {
      setLoading(false)
    }
  }, [])

  // Nothing was passed and nothing has been tried yet. This runs after render,
  // not during it, so the first paint is the honest "reading" state.
  useEffect(() => {
    if (authConfig || config || loading || loadError) return
    void load()
  }, [authConfig, config, loading, loadError, load])

  if (!config) {
    return (
      <div className="stack">
        <Panel
          className="settingsPanel"
          title="认证与安全配置"
          sub="登录、会话与安全策略。"
          actions={<Badge tone="neutral">只读</Badge>}
        >
          {loadError ? (
            <EmptyState
              icon={<Lock />}
              title="无法读取认证配置"
              description={`${loadError}。页面不显示默认值，以免把读不到的配置当成当前配置。`}
              action={
                <Button variant="primary" loading={loading} onClick={() => void load()}>
                  重新加载
                </Button>
              }
            />
          ) : (
            <p className="muted" role="status">正在读取认证配置…</p>
          )}
        </Panel>
      </div>
    )
  }

  const mode = (config.auth_mode || who?.auth_mode || '').toLowerCase()
  const {info, known} = modeInfo(mode)
  const Icon = info.icon
  const setupMode = config.setup_local_only
    ? config.setup_allowed_here ? '仅本机可完成初始化' : '仅限网关本机'
    : '未限制来源'

  return (
    <div className="stack">
      <Panel
        className="settingsPanel"
        title="认证与安全配置"
        sub="登录、会话与安全策略。"
        actions={<Badge tone="neutral">只读</Badge>}
        footer={<a className="linkInline" href="/admin-ui/profile">前往个人资料</a>}
      >
        <div className="settingsGroups">
          <section className="settingsGroup" aria-label="认证方式">
            <h3 className="settingsGroupTitle"><Icon aria-hidden />认证方式</h3>
            <div className="authModeCard">
              <span className={`authModeIcon ${info.tone}`} aria-hidden>
                <Icon />
              </span>
              <div className="authModeText">
                <div className="authModeTitle">
                  <b>{info.label}</b>
                  <Badge tone={info.tone}>{mode || '未返回'}</Badge>
                  {!known && <Badge tone="warning">未知模式</Badge>}
                </div>
                {/* An unknown mode is a warning, not a detail: keep its
                    explanation visible instead of folding it away. */}
                {!known && <p>{info.description}</p>}
              </div>
            </div>
          </section>

          <section className="settingsGroup" aria-label="登录与注册">
            <h3 className="settingsGroupTitle"><Lock aria-hidden />登录与注册</h3>
            <div className="settingsFacts">
              <Fact
                label="账号登录"
                value={<Badge tone={config.login_enabled ? 'success' : 'warning'}>{config.login_enabled ? '已启用' : '已关闭'}</Badge>}
              />
              <Fact
                label="公开注册"
                value={<Badge tone={config.registration_enabled ? 'success' : 'neutral'}>{config.registration_enabled ? '已开放' : '未开放'}</Badge>}
              />
              <Fact label="初始化状态" value={config.needs_setup ? '尚未完成' : '已完成'} />
              <Fact label="初始化来源限制" value={setupMode} />
            </div>
          </section>

          <section className="settingsGroup" aria-label="会话与密码">
            <h3 className="settingsGroupTitle"><ShieldCheck aria-hidden />会话与密码</h3>
            <div className="settingsFacts">
              <Fact label="访问凭证有效期" value={duration(config.access_ttl_seconds)} />
              <Fact label="密码最短长度" value={`${config.min_password_length} 位`} />
            </div>
          </section>

          <InfoDisclosure summary="高级信息与部署说明">
            <p>{info.description}</p>
            <p>
              配置来自启动配置与环境变量（如 <code>ADMIN_AUTH_MODE</code>），修改后需要重启网关。
            </p>
            <p>初始化只能在网关本机完成；首个超级管理员创建后，初始化入口关闭。</p>
          </InfoDisclosure>
        </div>
      </Panel>
    </div>
  )
}
