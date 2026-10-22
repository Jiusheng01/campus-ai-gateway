import {useCallback, useEffect, useState} from 'react'
import {Fingerprint, KeyRound, Lock, ShieldCheck, UserRound} from 'lucide-react'
import {getAuthConfig, type AuthConfig, type WhoAmI} from '../api'
import {Panel} from '../components/ui/Panel'
import {Badge} from '../components/ui/Badge'
import {Button} from '../components/ui/Button'
import {EmptyState} from '../components/ui/EmptyState'
import {roleSummary} from '../lib/roles'

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

function Fact({label, value, hint}: {label: string; value: React.ReactNode; hint?: string}) {
  return (
    <div className="settingFact">
      <span className="settingFactLabel">{label}</span>
      <b className="settingFactValue">{value}</b>
      {hint && <small className="settingFactHint">{hint}</small>}
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
      <Panel title="系统设置" sub="网关的启动配置。所有值都为只读。">
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
        title="系统设置"
        sub="以下内容来自网关的启动配置（ADMIN_AUTH_MODE 及其相关环境变量），修改后需要重启网关。本页面只读。"
      >
        <div className="settingsGroups">
          <section className="settingsGroup" aria-label="当前操作者">
            <h3 className="settingsGroupTitle"><UserRound aria-hidden />当前操作者</h3>
            <div className="settingsFacts">
              <Fact label="账号" value={<span className="mono">{who?.username || who?.user || '—'}</span>} />
              <Fact label="角色" value={roleSummary(who?.roles)} />
              <Fact label="权限范围" value={who?.roles?.length ? '由角色决定，见导航中的可用页面' : '未返回角色，按普通用户处理'} />
            </div>
            <p className="settingsNote">账号自身的资料、密码和设备管理在「个人」下的页面中维护，不在本页。</p>
          </section>

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
                <p>{info.description}</p>
              </div>
            </div>
          </section>

          <section className="settingsGroup" aria-label="登录与注册">
            <h3 className="settingsGroupTitle"><Lock aria-hidden />登录与注册</h3>
            <div className="settingsFacts">
              <Fact
                label="账号登录"
                value={<Badge tone={config.login_enabled ? 'success' : 'warning'}>{config.login_enabled ? '已启用' : '已关闭'}</Badge>}
                hint={config.login_enabled ? '登录入口可用' : '控制台没有本地登录入口'}
              />
              <Fact
                label="公开注册"
                value={<Badge tone={config.registration_enabled ? 'success' : 'neutral'}>{config.registration_enabled ? '已开放' : '未开放'}</Badge>}
                hint={config.registration_enabled ? '任何人可自助注册为普通用户' : '账号由管理员创建'}
              />
              <Fact
                label="初始化状态"
                value={config.needs_setup ? '尚未完成' : '已完成'}
                hint={config.needs_setup ? '还没有超级管理员' : '首个超级管理员已存在'}
              />
              <Fact label="初始化来源限制" value={setupMode} />
            </div>
          </section>

          <section className="settingsGroup" aria-label="会话与密码">
            <h3 className="settingsGroupTitle"><ShieldCheck aria-hidden />会话与密码</h3>
            <div className="settingsFacts">
              <Fact label="访问凭证有效期" value={duration(config.access_ttl_seconds)} hint="到期后由续期 Cookie 自动换取新凭证" />
              <Fact label="密码最短长度" value={`${config.min_password_length} 位`} hint="由网关的密码策略决定" />
            </div>
          </section>
        </div>
        <p className="settingsReadOnly">
          <Lock aria-hidden />
          全部字段为只读：接口没有提供修改系统配置的入口，本页不会显示无法保存的开关。
        </p>
      </Panel>
    </div>
  )
}
