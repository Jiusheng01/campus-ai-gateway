export type Provider={id:string;name:string;base_url:string;protocol:string;credential_env:string;status:number}
export type ModelConfig={id:string;alias:string;provider_id:string;provider_name:string;upstream_model:string;status:number}
export type ModelRoute={id:string;model_id:string;model_alias:string;provider_id:string;provider_name:string;upstream_model:string;priority:number;weight:number;timeout_ms:number;status:number}
// Tenant quota fields are tri-state, which is why they are nullable:
//   null -> inherit from the nearest ancestor that sets a value
//   0    -> explicitly unlimited
//   >0   -> this tenant's own limit
export type TenantQuotaValue=number|null
export type Tenant={
  id:string;code:string;name:string;status:number
  kind:string
  parent_id:string|null
  path:string
  depth:number
  daily_token_limit:TenantQuotaValue
  monthly_token_limit:TenantQuotaValue
  monthly_request_limit:TenantQuotaValue
  monthly_budget_micros:TenantQuotaValue
  soft_token_limit:TenantQuotaValue
  allowed_models:string[]|null
  rpm_limit:number
  tpm_limit:number
}
export type TenantKind='school'|'dept'|'teacher'|'student'|'tenant'

// The full replacement payload for POST /admin/tenants/{id}/quota-allocations.
// An omitted field means "inherit", the same as sending null.
export type TenantAllocation={
  daily_token_limit?:TenantQuotaValue
  monthly_token_limit?:TenantQuotaValue
  monthly_request_limit?:TenantQuotaValue
  monthly_budget_micros?:TenantQuotaValue
  soft_token_limit?:TenantQuotaValue
  allowed_models?:string[]|null
}

// What a tenant actually gets once inheritance has been applied, as opposed to
// what it explicitly sets. model_access_restricted separates "no allowlist
// applies" (every model) from "an allowlist applies and it is empty" (no model).
export type EffectiveLimits={
  tenant_id:string
  daily_token_limit:number
  monthly_token_limit:number
  monthly_request_limit:number
  monthly_budget_micros:number
  model_access_restricted:boolean
  allowed_models:string[]|null
  depth:number
  path:string
}
// The per-model quota axis, alongside the tenant one. The same tri-state
// convention applies to the limits: null inherits, 0 is unlimited, >0 is a cap.
//
// tenant_id is null for the system-wide pool of a model -- the pool shared by
// the whole campus, which is also the root of the model chain. It is not
// "no tenant": the two are different rows and the backend keys off exactly this
// distinction.
export type ModelQuota={
  tenant_id:string|null
  tenant_code?:string
  tenant_name?:string
  model_alias:string
  daily_token_limit:TenantQuotaValue
  monthly_token_limit:TenantQuotaValue
}
export type ModelQuotaAllocation={
  tenant_id?:string|null
  model_alias:string
  daily_token_limit?:TenantQuotaValue
  monthly_token_limit?:TenantQuotaValue
}
// The resolved view of one model for one tenant. set_by names the level the
// value came from, so "I set 300 but it stops at 100" is answerable without a
// database client; own distinguishes "I configured this" from "I was given it".
export type EffectiveModelQuota={
  model_alias:string
  daily_token_limit:number
  monthly_token_limit:number
  set_by:string
  own:boolean
}
export type ModelQuotaView={quota:ModelQuota;effective:EffectiveModelQuota[]}
export type TenantAllocationView={tenant:Tenant;effective:EffectiveLimits;effective_models?:EffectiveModelQuota[]}
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
}

export type AdminUserView={id:string;username:string;display_name:string;roles:string[]}
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
const AUTH_PATHS=['/admin/auth-config','/admin/login','/admin/setup','/admin/auth/refresh','/admin/auth/logout','/admin/logout']
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

// --- account administration ------------------------------------------------
//
// Reachable only by a super_admin. The server refuses every other role with a
// 403, so hiding the menu entry is a convenience, not the security boundary.

export type AdminAccount={
  id:string
  username:string
  display_name:string
  roles:string[]
  status:number
  // null means the account reaches every tenant.
  scope_tenant_id:string|null
  last_login_at:string
  created_at:string
}

export const ADMIN_ROLES=['super_admin','gateway_admin','tenant_admin','auditor','operator'] as const

// ROLE_LABELS is what the operator sees. The stored values stay in English so
// they line up with the server-side allow-list.
export const ROLE_LABELS:Record<string,string>={
  super_admin:'超级管理员',
  gateway_admin:'网关运维',
  tenant_admin:'租户管理',
  auditor:'审计只读',
  operator:'值班操作',
}

export function roleLabel(role:string){return ROLE_LABELS[role]||role}

export async function listAdminUsers(){
  const d=await api<{data:AdminAccount[]}>('/admin/admin-users')
  return d.data||[]
}

export async function createAdminUser(input:{username:string;display_name:string;password:string;roles:string[];scope_tenant_id?:string|null}){
  return api<AdminAccount>('/admin/admin-users',{method:'POST',body:JSON.stringify(input)})
}

export async function updateAdminUser(id:string,input:{roles?:string[];status?:number}){
  return api<AdminAccount>(`/admin/admin-users/${encodeURIComponent(id)}`,{method:'PATCH',body:JSON.stringify(input)})
}

// setAdminUserScope lives on its own route rather than in the PATCH body: a
// pointer field cannot tell "clear the scope" apart from "leave it alone", and
// here that difference is between confining an account and granting it global
// reach.
export async function setAdminUserScope(id:string,scopeTenantId:string|null){
  return api<AdminAccount>(`/admin/admin-users/${encodeURIComponent(id)}/scope`,{
    method:'POST',body:JSON.stringify({scope_tenant_id:scopeTenantId}),
  })
}

// ---------------------------------------------------------------------------
// Tenant hierarchy
// ---------------------------------------------------------------------------

export const TENANT_KINDS=['school','dept','teacher','student','tenant'] as const

export const TENANT_KIND_LABELS:Record<string,string>={
  school:'学校',
  dept:'院系',
  teacher:'教师',
  student:'学生',
  tenant:'通用',
}

export function tenantKindLabel(kind:string){return TENANT_KIND_LABELS[kind]||kind}

export async function getTenantAllocation(id:string){
  return api<TenantAllocationView>(`/admin/tenants/${encodeURIComponent(id)}/quota-allocations`)
}

export async function allocateTenantQuota(id:string,input:TenantAllocation){
  return api<TenantAllocationView>(`/admin/tenants/${encodeURIComponent(id)}/quota-allocations`,{
    method:'POST',body:JSON.stringify(input),
  })
}

export async function moveTenant(id:string,parentId:string|null){
  return api<Tenant>(`/admin/tenants/${encodeURIComponent(id)}/move`,{
    method:'POST',body:JSON.stringify({parent_id:parentId}),
  })
}

// ---------------------------------------------------------------------------
// Per-model quota pools
// ---------------------------------------------------------------------------

export async function listModelQuotas(){
  const r=await api<{data:ModelQuota[]}>('/admin/model-quotas')
  return r.data
}

export async function upsertModelQuota(input:ModelQuotaAllocation){
  return api<ModelQuotaView>('/admin/model-quotas',{method:'PUT',body:JSON.stringify(input)})
}

// Deleting a row is how a subject goes back to inheriting, so removal is a
// first-class operation rather than "set it to null". An absent tenant_id means
// the system-wide pool -- the same meaning it has on write.
export async function deleteModelQuota(tenantId:string|null,modelAlias:string){
  const q=new URLSearchParams()
  if(tenantId)q.set('tenant_id',tenantId)
  q.set('model_alias',modelAlias)
  return api<{removed:boolean}>(`/admin/model-quotas?${q.toString()}`,{method:'DELETE'})
}
