import {ApiError} from '../api'

// The API answers in English because it is a machine interface; the console is
// a product. Mapping codes here rather than passing the server text through is
// what keeps a message change on the backend from turning a clear Chinese
// sentence into an English one on the login screen.
const AUTH_ERRORS: Record<string, string> = {
  invalid_credentials: '用户名或密码错误',
  unauthenticated: '请先登录',
  session_expired: '登录状态已过期，请重新登录。',
  session_revoked: '会话已被撤销，请重新登录。',
  refresh_replay_detected: '检测到登录凭据重放，所有会话已被撤销，请重新登录。',
  account_disabled: '当前账号已停用，请联系管理员。',
  account_not_found: '账号不存在',
  admin_forbidden: '没有权限执行该操作',
  password_policy_violation: '密码不符合安全要求：至少 10 位，且需包含字母、数字、符号中的至少两类。',
  password_unchanged: '新密码不能与当前密码相同',
  invalid_request: '请检查填写内容',
  too_many_attempts: '尝试次数过多，请稍后再试。',
  registration_disabled: '当前暂未开放注册。',
  registration_rate_limited: '注册请求过于频繁，请稍后重试。',
  registration_unavailable: '注册服务暂时不可用，请稍后重试。',
  setup_required: '系统尚未完成初始化，请先创建管理员。',
  username_taken: '该用户名已被使用，请换一个。',
  account_created_login_failed: '账号已创建成功，但自动登录失败。请直接使用刚设置的密码登录。',
  last_super_admin: '系统至少需要保留一个启用的超级管理员。',
  csrf_rejected: '请求被安全策略拒绝，请刷新页面后重试。',
  admin_api_disabled: '管理接口未启用',
}

/**
 * friendlyError turns an API error into something an administrator can act on.
 *
 * A disabled account is the one case worth calling out separately: the generic
 * "username or password error" would leave an operator who has been switched
 * off wondering what they mistyped.
 */
export function friendlyError(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    // A confirmation mismatch is rejected by the handler as a generic bad
    // request, so it is described here instead.
    if (err.code === 'invalid_request' && err.status === 400) return fallback
    return AUTH_ERRORS[err.code] || fallback
  }
  return err instanceof Error && err.message ? err.message : fallback
}
