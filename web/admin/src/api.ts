export type Provider={id:string;name:string;base_url:string;protocol:string;credential_env:string;status:number}
export type ModelConfig={id:string;alias:string;provider_id:string;provider_name:string;upstream_model:string;status:number}
export type ModelRoute={id:string;model_id:string;model_alias:string;provider_id:string;provider_name:string;upstream_model:string;priority:number;weight:number;timeout_ms:number;status:number}
export type Tenant={id:string;code:string;name:string;status:number;daily_token_limit:number;monthly_token_limit:number;monthly_request_limit:number;monthly_budget_micros:number}
export type TenantUsage={tenant_id:string;daily_requests:number;monthly_requests:number;daily_tokens:number;monthly_tokens:number}
export type ApiKey={id:string;tenant_id:string;tenant_code:string;tenant_name:string;name:string;key_prefix:string;status:number;expire_at?:string;last_used_at?:string;allowed_models:string[];allowed_ips:string[];rpm_limit:number;tpm_limit:number;max_concurrency:number;route_group:string}
export type RequestLog={request_id:string;tenant_id:string;api_key_id:string;client_ip:string;requested_model:string;actual_model:string;provider:string;upstream_model:string;route_priority:number;attempts:number;failover:boolean;stream:boolean;input_tokens:number;output_tokens:number;total_tokens:number;latency_ms:number;status_code:number;error_code:string;created_at:string}
export type AdminAudit={id:number;request_id:string;actor:string;action:string;resource:string;status_code:number;client_ip:string;created_at:string}
export type SecurityEvent={id:number;request_id:string;tenant_id:string;api_key_id:string;event_type:string;client_ip:string;requested_model:string;detail:string;created_at:string}
export type ProviderStat={provider:string;requests:number;success_rate:number;avg_latency_ms:number}
export type DashboardStatus={gateway:boolean;postgres:boolean;redis:boolean;thinkwatch:boolean;stats:{today_requests:number;today_tokens:number;today_errors:number;avg_latency_ms:number;p95_latency_ms:number;failover_count:number};provider_stats:ProviderStat[]}
export type WhoAmI={user:string;username?:string;user_id?:string;session_id?:string;roles:string[];auth_mode:string}

// ---------------------------------------------------------------------------
// Authentication
//
// The access token lives in a module-level variable and nowhere else. It is
// never written to localStorage, sessionStorage or IndexedDB, so a script
// injected into the page cannot read a credential out of persistent storage.
// The refresh token is an HttpOnly cookie the browser sends automatically and
// JavaScript cannot read at all.
// ---------------------------------------------------------------------------

export type AuthConfig={
  auth_mode:string
  login_enabled:boolean
  needs_setup:boolean
  setup_local_only:boolean
  setup_allowed_here:boolean
  min_password_length:number
  access_ttl_seconds:number
  /**
   * Whether this deployment accepts public sign-ups. It is false while the
   * first super administrator does not exist yet, because setup — not
   * registration — creates that account.
   */
  registration_enabled:boolean
}

export type AdminUserView={id:string;username:string;display_name:string;roles:string[]}

/**
 * A row in the user management table. Deliberately narrow: the API never sends
 * a password hash, a refresh hash or an auth_version, so there is nothing here
 * for a script on the page to leak.
 */
export type AccountUser={
  id:string
  username:string
  display_name:string
  roles:string[]
  status:number
  last_login_at:string|null
  created_at:string
}

export type AccountUserStats={
  total:number
  members:number
  admins:number
  disabled:number
}

export type LoginResult={access_token:string;expires_at:string;user:AdminUserView}
export type AdminSessionView={
  id:string;current:boolean;login_method:string;ip:string;user_agent:string
  created_at:string;last_active_at:string;expires_at:string
}

export class ApiError extends Error{
  status:number
  code:string
  constructor(message:string,status:number,code=''){
    super(message);this.name='ApiError';this.status=status;this.code=code
  }
}

let accessToken=''
let accessExpiresAt=0
let refreshing:Promise<boolean>|null=null
let onUnauthenticated:(reason:'expired'|'revoked'|'unknown')=>void=()=>{}

// The active authentication mode, learned from /admin/auth-config at boot. Only
// "session" mode has an access token and a refresh cookie; the other modes are
// driven entirely by the surrounding infrastructure, so the client must not try
// to refresh or store anything for them.
let authMode=''

export function setUnauthenticatedHandler(fn:(reason:'expired'|'revoked'|'unknown')=>void){onUnauthenticated=fn}
export function setAuthMode(mode:string){authMode=(mode||'').toLowerCase()}
export function getAuthMode(){return authMode}
export function isSessionMode(){return authMode==='session'}
export function getAccessToken(){return accessToken}
export function accessExpiry(){return accessExpiresAt}
export function hasSession(){return accessToken!==''}

function applySession(token:string,expiresAt:string){
  accessToken=token||''
  accessExpiresAt=expiresAt?Date.parse(expiresAt):0
}
export function clearSession(){accessToken='';accessExpiresAt=0}

// Endpoints that must never trigger the refresh-and-retry loop: they either are
// the refresh itself or are meant to fail with a visible message.
const AUTH_PATHS=['/admin/auth-config','/admin/login','/admin/setup','/auth/register','/admin/auth/refresh','/admin/auth/logout','/admin/logout']
function isAuthEndpoint(path:string){
  const base=path.split('?')[0]
  return AUTH_PATHS.some(p=>base===p)
}

// refreshSession exchanges the HttpOnly refresh cookie for a new access token.
// Concurrent callers share one in-flight request, so a page that fires several
// API calls at once does not trigger a refresh storm — which the server would
// treat as refresh-token reuse and answer by revoking the whole session family.
export function refreshSession():Promise<boolean>{
  if(refreshing)return refreshing
  const p=(async()=>{
    try{
      const r=await fetch('/admin/auth/refresh',{method:'POST',credentials:'same-origin'})
      if(!r.ok){clearSession();return false}
      const d=await r.json() as LoginResult
      applySession(d.access_token,d.expires_at)
      return true
    }catch{clearSession();return false}
  })()
  refreshing=p
  p.finally(()=>{if(refreshing===p)refreshing=null})
  return p
}

export async function api<T>(path:string,opt:RequestInit={},retry=true):Promise<T>{
  const headers=new Headers(opt.headers||{})
  if(accessToken)headers.set('Authorization','Bearer '+accessToken)
  if(opt.body&&!headers.has('Content-Type'))headers.set('Content-Type','application/json')
  const r=await fetch(path,{...opt,headers,credentials:'same-origin'})
  const txt=await r.text()
  let data:any={}
  try{data=txt?JSON.parse(txt):{}}catch{data={raw:txt}}
  const code=data?.error?.code||''
  const message=data?.error?.message||('HTTP '+r.status)

  if(r.status===401&&retry&&!isAuthEndpoint(path)){
    // Only session mode owns a refresh cookie. In trusted-header mode the
    // identity is injected by the campus reverse proxy, and there is nothing to
    // refresh — attempting it would just produce a confusing 503.
    if(isSessionMode()&&await refreshSession())return api<T>(path,opt,false)
    clearSession()
    onUnauthenticated(code==='session_revoked'?'revoked':'expired')
    throw new ApiError(message,401,code)
  }
  if(!r.ok)throw new ApiError(message,r.status,code)
  return data as T
}

export async function download(path:string,filename:string){
  const authHeaders=()=>{
    const h=new Headers()
    if(accessToken)h.set('Authorization','Bearer '+accessToken)
    return h
  }
  let r=await fetch(path,{headers:authHeaders(),credentials:'same-origin'})
  if(r.status===401&&!isAuthEndpoint(path)&&isSessionMode()&&await refreshSession()){
    r=await fetch(path,{headers:authHeaders(),credentials:'same-origin'})
  }
  if(!r.ok){let msg='HTTP '+r.status;try{const d=await r.json();msg=d?.error?.message||msg}catch{}throw new ApiError(msg,r.status)}
  const blob=await r.blob();const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=filename;a.click();URL.revokeObjectURL(url)
}

// --- auth endpoints --------------------------------------------------------

export async function getAuthConfig(){
  const r=await fetch('/admin/auth-config',{credentials:'same-origin'})
  const d=await r.json().catch(()=>({}))
  if(!r.ok)throw new ApiError(d?.error?.message||'无法读取登录配置',r.status,d?.error?.code||'')
  return d as AuthConfig
}

export async function login(username:string,password:string){
  const r=await fetch('/admin/login',{
    method:'POST',credentials:'same-origin',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify({username,password}),
  })
  const d=await r.json().catch(()=>({}))
  if(!r.ok)throw new ApiError(d?.error?.message||'登录失败',r.status,d?.error?.code||'')
  const out=d as LoginResult
  applySession(out.access_token,out.expires_at)
  return out
}

export async function setupAdmin(input:{username:string;display_name:string;password:string;confirm_password:string}){
  const r=await fetch('/admin/setup',{
    method:'POST',credentials:'same-origin',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify(input),
  })
  const d=await r.json().catch(()=>({}))
  if(!r.ok)throw new ApiError(d?.error?.message||'初始化失败',r.status,d?.error?.code||'')
  const out=d as LoginResult
  applySession(out.access_token,out.expires_at)
  return out
}

/**
 * register creates a public member account and signs it in.
 *
 * The server fixes the role list to ["member"] and refuses a body that carries
 * role fields at all, so this function sends only the four fields it owns. The
 * response is the same shape as a login: an access token plus the refresh
 * cookie, which means a new user lands in the console without retyping the
 * password they just chose.
 */
export async function register(input:{username:string;display_name:string;password:string;confirm_password:string}){
  const r=await fetch('/auth/register',{
    method:'POST',credentials:'same-origin',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify(input),
  })
  const d=await r.json().catch(()=>({}))
  if(!r.ok)throw new ApiError(d?.error?.message||'注册失败',r.status,d?.error?.code||'')
  const out=d as LoginResult
  applySession(out.access_token,out.expires_at)
  return out
}

// logout calls the cookie-reachable path so the browser actually attaches the
// refresh cookie. The server revokes the session and clears the cookie.
export async function logout(){
  try{
    await fetch('/admin/auth/logout',{method:'POST',credentials:'same-origin'})
  }finally{
    clearSession()
  }
}

export async function changePassword(currentPassword:string,newPassword:string){
  return api<{ok:boolean;message:string}>('/admin/password',{
    method:'POST',
    body:JSON.stringify({current_password:currentPassword,new_password:newPassword}),
  })
}

export async function listSessions(){
  const d=await api<{data:AdminSessionView[]}>('/admin/sessions')
  return d.data||[]
}

// whoami resolves the current administrator identity. It is used by
// trusted-header mode, where the campus reverse proxy injects the identity and
// there is no login form to submit.
export async function whoami(){
  return api<WhoAmI>('/admin/whoami',{},false)
}

export async function revokeSession(id:string){
  return api<{ok:boolean}>(`/admin/sessions/${encodeURIComponent(id)}/revoke`,{method:'POST'})
}

// --- user management (super_admin only) -----------------------------------

/**
 * listUsers reads the account table. The server answers 403 for every role
 * other than super_admin, so the page it feeds is gated on the same thing.
 */
export async function listUsers(){
  const d=await api<{data:AccountUser[];stats:AccountUserStats}>('/admin/users')
  return {data:d.data||[],stats:d.stats}
}

export type UserPatch={
  display_name?:string
  roles?:string[]
  status?:number
}

/**
 * updateUser changes an account's roles or status.
 *
 * The server bumps auth_version and revokes that account's sessions, so the
 * target has to sign in again before the change applies to them — which is the
 * point: authority taken away must not keep working on the strength of a token
 * issued before it was taken.
 */
export async function updateUser(id:string,patch:UserPatch){
  return api<AccountUser>(`/admin/users/${encodeURIComponent(id)}`,{
    method:'PATCH',
    body:JSON.stringify(patch),
  })
}
