import {Fingerprint, KeyRound, ShieldCheck} from 'lucide-react'
import type {AuthConfig, WhoAmI} from '../api'
import {Panel} from '../components/ui/Panel'
import {Badge} from '../components/ui/Badge'

const MODE_INFO: Record<string, {label: string; description: string; icon: typeof KeyRound; tone: 'success' | 'info' | 'warning'}> = {
  session: {
    label: '账号密码登录',
    description: '使用账号密码登录。访问凭证只保存在页面内存中，刷新凭证走 HttpOnly Cookie，不写入浏览器本地存储。',
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

/**
 * System settings describe the deployment, not the person using it.
 *
 * Password and device management moved to 安全与访问: they are the account's
 * own credentials, and mixing them with the gateway's authentication mode made
 * the page read as two unrelated halves.
 */
export function SettingsPage({who, authConfig}: {who: WhoAmI | null; authConfig: AuthConfig | null}) {
  const mode = (authConfig?.auth_mode || who?.auth_mode || '').toLowerCase()
  const info = MODE_INFO[mode] || {
    label: authConfig?.auth_mode || '未知',
    description: '当前认证模式未在本控制台的已知模式列表中。请联系校园信息化管理部门确认网关配置。',
    icon: ShieldCheck,
    tone: 'warning' as const,
  }
  const Icon = info.icon

  return (
    <div className="stack">
      <Panel title="认证方式" sub="由网关的 ADMIN_AUTH_MODE 决定，修改后需要重启网关生效。">
        <div className="authModeCard">
          <span className={`authModeIcon ${info.tone}`} aria-hidden>
            <Icon />
          </span>
          <div className="authModeText">
            <div className="authModeTitle">
              <b>{info.label}</b>
              <Badge tone={info.tone}>{mode || '未知'}</Badge>
            </div>
            <p>{info.description}</p>
          </div>
        </div>
      </Panel>
    </div>
  )
}
