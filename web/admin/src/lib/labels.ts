import type {Tone} from '../components/ui/Badge'

/** Chinese labels for audit actions recorded by the control plane. */
const AUDIT_LABELS: Record<string, string> = {
  GET: '读取',
  POST: '创建 / 提交',
  PUT: '修改',
  DELETE: '删除',
  admin_setup: '初始化管理员',
  admin_setup_failed: '初始化失败',
  admin_setup_rejected: '初始化被拒绝',
  admin_setup_remote_rejected: '远程初始化被拒绝',
  admin_login: '登录成功',
  admin_login_failed: '登录失败',
  admin_login_throttled: '登录被限流',
  admin_logout: '退出登录',
  admin_password_change: '修改密码',
  admin_password_change_failed: '修改密码失败',
  admin_password_change_rejected: '修改密码被拒绝',
  admin_session_revoked: '撤销登录设备',
  admin_refresh_replay_detected: '检测到登录凭据重放',
}

export const auditLabel = (action: string) => AUDIT_LABELS[action] || action

const AUDIT_DANGER = new Set(['admin_setup_remote_rejected', 'admin_refresh_replay_detected', 'DELETE'])
const AUDIT_WARNING = new Set(['admin_login_failed', 'admin_login_throttled', 'admin_setup_failed', 'admin_password_change_failed'])

export function auditTone(action: string): Tone {
  if (AUDIT_DANGER.has(action)) return 'danger'
  if (AUDIT_WARNING.has(action)) return 'warning'
  return 'neutral'
}

/** Chinese labels and severity for data-plane security events. */
const SECURITY_LABELS: Record<string, {label: string; tone: Tone}> = {
  invalid_api_key: {label: '无效 API Key', tone: 'danger'},
  api_key_expired: {label: 'API Key 已过期', tone: 'danger'},
  ip_forbidden: {label: '来源 IP 被拒绝', tone: 'danger'},
  model_forbidden: {label: '模型未授权', tone: 'danger'},
  rpm_exceeded: {label: 'RPM 超限', tone: 'warning'},
  tpm_exceeded: {label: 'TPM 超限', tone: 'warning'},
  concurrency_exceeded: {label: '并发超限', tone: 'warning'},
  tenant_request_quota_exceeded: {label: '租户请求配额超限', tone: 'warning'},
  tenant_token_quota_exceeded: {label: '租户 Token 配额超限', tone: 'warning'},
}

export const securityLabel = (type: string) => SECURITY_LABELS[type]?.label || type
export const securityTone = (type: string): Tone => SECURITY_LABELS[type]?.tone || 'neutral'
