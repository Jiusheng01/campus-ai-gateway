export type CredentialSummary={source_type:string;configured:boolean;last4:string}
export type ProviderTestResult={at:string;ok:boolean;status_code:number;latency_ms:number;upstream_model:string;error_kind?:string}
export type Provider={id:string;name:string;base_url:string;protocol:string;provider_type:string;description:string;credential_env:string;credential?:CredentialSummary;credential_secret?:string;status:number;last_test?:ProviderTestResult;candidate_model_count?:number;referenced_route_count?:number;referenced_model_count?:number}
/** What one channel delete actually removed; the counts are the confirmation, not a preview. */
export type ProviderDeleteResult={deleted_routes:number;deleted_virtual_models:number;deleted_provider_models:number}
export type ModelConfig={id:string;alias:string;display_name:string;description:string;status:number;config_version:number;route_count?:number;enabled_route_count?:number;statically_routable?:boolean;credential_configured?:boolean;routing_mode_summary?:string}
export type ModelRoute={id:string;model_id:string;model_alias:string;provider_id:string;provider_name:string;upstream_model:string;priority:number;weight:number;timeout_ms:number;status:number}
export type RouteView=ModelRoute&{model_display_name:string;config_version:number;provider_status:number;credential_configured:boolean}
export type ModelDetail={model:ModelConfig;routes:RouteView[];version:number}
export type PublishResult={model:ModelConfig;route:ModelRoute;created:boolean;route_created:boolean;version:number}
export type ProviderImpactModel={model_id:string;alias:string;routes_to_provider:number;remaining_routes:number;orphaned:boolean}
export type ProviderImpact={routes:number;provider_models:number;orphaned_models:number;affected_models:ProviderImpactModel[]}
export type ModelImpact={alias:string;enabled_routes:number;enabled_providers:number;explicit_keys:number;wildcard_keys:number}
export type ModelAuthorization={api_key_id:string;name:string;tenant_id:string;tenant_name:string;status:number;wildcard:boolean;allowed:boolean;last_used_at?:string}
export type AuthorizationResult={api_key_id:string;outcome:'added'|'already_allowed'|'removed'|'not_present'|'wildcard_refused'|'not_found'}

/** ProviderModel is one entry in a channel's own list of upstream names. */
export type ProviderModelLink={route_id:string;model_id:string;alias:string;route_status:number}
export type ProviderModel={id:string;provider_id:string;upstream_model:string;source:'preset'|'fetched'|'manual';status:number;links?:ProviderModelLink[]}

/**
 * CatalogProvider is one channel offering an upstream model.
 *
 * `opened` is narrower than the row's: a Virtual Model may exist and already
 * route to another channel, in which case opening this one appends a Route.
 */
export type CatalogProvider={id:string;name:string;provider_model_id:string;opened:boolean;model_id?:string;route_id?:string}

/** One Model Catalog row: an upstream name grouped across the channels offering it. */
export type CatalogModel={upstream_model:string;opened:boolean;providers:CatalogProvider[]}

/**
 * CatalogOpenResult reports what the open action changed. Both flags false is
 * the idempotent repeat — the model and Route already covered this binding.
 */
export type CatalogOpenResult={model:ModelConfig;route:ModelRoute;created:boolean;route_created:boolean}
export type Tenant={id:string;code:string;name:string;status:number;daily_token_limit:number;monthly_token_limit:number;monthly_request_limit:number;monthly_budget_micros:number;rpm_limit:number;tpm_limit:number;max_concurrency:number;allowed_models:string[]}
export type TenantUsage={tenant_id:string;daily_requests:number;monthly_requests:number;daily_tokens:number;monthly_tokens:number}
export type ApiKey={id:string;tenant_id:string;tenant_code:string;tenant_name:string;name:string;key_prefix:string;status:number;expire_at?:string;last_used_at?:string;allowed_models:string[];allowed_ips:string[];route_group:string;usage_requests:number;usage_tokens:number}
export type RequestAttempt={attempt_no:number;provider:string;upstream_model:string;outcome:string;status_code:number;latency_ms:number;error_kind:string}
export type RequestLog={request_id:string;tenant_id:string;api_key_id:string;client_ip:string;requested_model:string;actual_model:string;provider:string;upstream_model:string;route_priority:number;attempts:number;failover:boolean;stream:boolean;input_tokens:number;output_tokens:number;total_tokens:number;latency_ms:number;status_code:number;error_code:string;created_at:string}
export type AdminAudit={id:number;request_id:string;actor:string;action:string;resource:string;status_code:number;client_ip:string;created_at:string}
export type SecurityEvent={id:number;request_id:string;tenant_id:string;api_key_id:string;event_type:string;client_ip:string;requested_model:string;detail:string;created_at:string}
export type ProviderStat={provider:string;requests:number;success_rate:number;avg_latency_ms:number}

/**
 * Which data plane the gateway is running.
 *
 * There is one: "direct" resolves the model against the channel configuration
 * and dials it in-process, so there is no second process to run and no
 * configuration to synchronise.
 */
export type DataPlane='direct'

export type CircuitView={provider:string;state:'closed'|'open'|'half-open';failures:number;retry_in_ms:number}
export type RouteCacheStatus={built_at:string|null;stale:boolean;last_error_kind?:string;generation:number}
export type DashboardStatus={gateway:boolean;postgres:boolean;redis:boolean;data_plane?:DataPlane;stats:{today_requests:number;today_tokens:number;today_errors:number;avg_latency_ms:number;p95_latency_ms:number;failover_count:number};provider_stats:ProviderStat[];circuits?:CircuitView[];route_cache?:RouteCacheStatus;config_counts?:{models:number;enabled_models:number;providers:number;enabled_providers:number}}
export type WhoAmI={user:string;username?:string;user_id?:string;session_id?:string;roles:string[];auth_mode:string}

/**
 * RequestLogSummary aggregates the whole filtered set, not the page on screen.
 *
 * The distinction matters to every reader: `requests` is how many rows match
 * the current filter, not how many are displayed.
 */
export type RequestLogSummary={requests:number;errors:number;input_tokens:number;output_tokens:number;total_tokens:number;avg_latency_ms:number;p95_latency_ms:number;failover_count:number}

/**
 * QuotaDimension is one quota band: what the tenant has used, its configured
 * limit, and the share of the limit that represents.
 *
 * It is null on the wire when the tenant has no limit for that band. A 0% would
 * read as "the quota is untouched", which is the opposite of "there is no quota
 * to touch", so the two cases are kept apart rather than flattened.
 */
export type QuotaDimension={used:number;limit:number;pct:number}

/**
 * RequestLogQuota is the quota consumption for the one tenant the filter
 * describes. Every band is null both when that band has no limit and when the
 * filter spans more than one tenant, in which case there is no single
 * denominator to quote a share against.
 */
export type RequestLogQuota={daily_token:QuotaDimension|null;monthly_token:QuotaDimension|null;monthly_request:QuotaDimension|null}

/** One API key's share of the filtered usage; `quota_share` is null for the same reasons a quota band is. */
export type APIKeyUsage={api_key_id:string;name:string;key_prefix:string;requests:number;tokens:number;quota_share:number|null}

/** The totals are the summary's, so the strip above the table cannot describe a different set of rows than the summary endpoint would. */
export type RequestLogUsageTotals=RequestLogSummary&{quota:RequestLogQuota}

/**
 * RequestLogUsage is the aggregate for one filter selection: the totals, the
 * tenant quota consumption, and the same usage grouped by API key.
 *
 * All three are computed over the identical predicate as the log table — the
 * same query string is sent to every endpoint on the page — so none of them can
 * describe a different selection than the list beneath them.
 */
export type RequestLogUsage={totals:RequestLogUsageTotals;by_key:APIKeyUsage[]}

/**
 * One point of the traffic trend.
 *
 * `bucket` is a wall-clock label the server formatted in this browser's UTC
 * offset, not an instant. It is rendered verbatim — parsing it back into a Date
 * would shift it a second time and label every bar with the wrong hour.
 */
export type TrendBucket={bucket:string;requests:number;tokens:number;errors:number;avg_latency_ms:number;p95_latency_ms:number}

export type TrendSeries={data:TrendBucket[];bucket:'hour'|'day';from:string;to:string}

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
  /** The Account's Tenant Membership. Empty means unassigned, not a Tenant named "". */
  tenant_id:string
  tenant_name:string
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

/**
 * The narrow model projection the connectivity test needs.
 *
 * It is not a ModelConfig: an operator may run a diagnostic without being
 * allowed to read or edit the model configuration, so the gateway answers this
 * from a dedicated endpoint that carries only what the picker renders.
 */
export type ModelTestOption={id:string;alias:string;status:number}
export type AdminSessionView={
  id:string;current:boolean;login_method:string;ip:string;user_agent:string
  created_at:string;last_active_at:string;expires_at:string
}

export class ApiError extends Error{
  status:number
  code:string
  /**
   * The server's error object beyond code and message, when it sent one.
   *
   * A refusal such as "this account still owns 3 keys" carries the number as a
   * field. The console renders that instead of parsing prose, so a wording
   * change on the server cannot silently break the message.
   */
  details:Record<string,unknown>
  constructor(message:string,status:number,code='',details:Record<string,unknown>={}){
    super(message);this.name='ApiError';this.status=status;this.code=code;this.details=details
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
    throw new ApiError(message,401,code,data?.error||{})
  }
  if(!r.ok)throw new ApiError(message,r.status,code,data?.error||{})
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

/**
 * ConsoleMe is the personal workspace's identity projection.
 *
 * `tenant_id` and `tenant_name` are empty when the Account has no Tenant
 * Membership — an unassigned state, not a Tenant named "". `workspace_usable`
 * is the same fact as a boolean, so the overview can say plainly that the
 * account is waiting for assignment instead of rendering an empty workspace.
 */
export type ConsoleMe={
  account_id:string
  username:string
  display_name:string
  roles:string[]
  status:number
  tenant_id:string
  tenant_name:string
  /**
   * The Tenant's model allowlist: ["*"], an explicit list, or [] (none).
   * This is the ceiling the create drawer offers; the gateway enforces the same
   * list again inside the create transaction.
   */
  allowed_models:string[]
  workspace_usable:boolean
}

export async function getConsoleMe(){
  return api<ConsoleMe>('/api/console/me',{},false)
}

/**
 * ConsoleKey is one row of the member's own key list.
 *
 * It deliberately has no `api_key` field: the create response is the only
 * place the full credential exists. `key_prefix` is enough to recognise a key
 * and is what the table shows.
 */
export type ConsoleKey={
  id:string
  tenant_id:string
  tenant_code:string
  tenant_name:string
  name:string
  key_prefix:string
  status:number
  expire_at?:string
  last_used_at?:string
  allowed_models:string[]
  allowed_ips:string[]
  created_at:string
  usage_requests:number
  usage_tokens:number
}

export async function listConsoleKeys(){
  const d=await api<{data:ConsoleKey[]}>('/api/console/keys')
  return d.data||[]
}

/**
 * createConsoleKey returns the full key exactly once.
 *
 * Only name and model scope are sent: owner and tenant are derived by the
 * gateway from the session and the Tenant Membership, and the request type has
 * no field that could override them.
 */
export async function createConsoleKey(input:{name:string;allowed_models:string[]}){
  return api<ConsoleKey&{api_key:string}>('/api/console/keys',{
    method:'POST',
    body:JSON.stringify(input),
  })
}

/**
 * ConsoleKeyUpdate is the whole editable surface of a personal key.
 *
 * There is no tenant_id, owner_user_id or status here. The first two are facts
 * of the row the gateway derives, and pausing is a separate operation so that
 * an edit can never resume a key by accident.
 */
export type ConsoleKeyUpdate={
  name:string
  allowed_models:string[]
  allowed_ips:string[]
  /** null clears the expiry; omit only when the caller does not mean to replace it. */
  expire_at:string|null
}

export async function getConsoleKey(id:string){
  return api<ConsoleKey>(`/api/console/keys/${encodeURIComponent(id)}`)
}

export async function updateConsoleKey(id:string,input:ConsoleKeyUpdate){
  return api<ConsoleKey>(`/api/console/keys/${encodeURIComponent(id)}`,{
    method:'PUT',
    body:JSON.stringify(input),
  })
}

/**
 * setConsoleKeyStatus pauses or resumes a key. The credential is untouched, so
 * the same key works again after re-enabling.
 */
export async function setConsoleKeyStatus(id:string,status:1|2){
  return api<{ok:boolean;id:string;status:number}>(`/api/console/keys/${encodeURIComponent(id)}/status`,{
    method:'PUT',
    body:JSON.stringify({status}),
  })
}

/**
 * deleteConsoleKey removes the row permanently. Request Records survive through
 * their owner snapshot; the credential does not.
 */
export async function deleteConsoleKey(id:string){
  return api<{ok:boolean;id:string;deleted:boolean}>(`/api/console/keys/${encodeURIComponent(id)}`,{
    method:'DELETE',
  })
}

/**
 * ConsoleModel is the member-visible view of a Virtual Model.
 *
 * There is deliberately no Provider, Route, credential or upstream name here:
 * those are management facts. `available` is true for every returned row — the
 * catalog only contains statically routable models — and is carried so the page
 * states availability instead of inferring it from presence.
 */
export type ConsoleModel={
  alias:string
  display_name:string
  description:string
  available:boolean
}

/**
 * ConsoleModels is the personal model catalog.
 *
 * `empty_reason` names why `models` is empty, so the page can distinguish an
 * unassigned account (`no_tenant`), a Tenant that permits nothing
 * (`tenant_allows_none`), and an empty deployment (`no_eligible_models`). It is
 * absent when there is at least one model.
 */
export type ConsoleModels={
  models:ConsoleModel[]
  empty_reason?:'no_tenant'|'tenant_allows_none'|'no_eligible_models'
}

export async function getConsoleModels(){
  return api<ConsoleModels>('/api/console/models',{},false)
}

/**
 * ConsoleUsageTotals is one period of the member's own traffic.
 *
 * `successes + failures === requests` always holds, and `error_rate` is the
 * failure share. It is a percentage, not a fraction, because every other share
 * the console renders (quota bands) is already in percent.
 */
export type ConsoleUsageTotals={
  requests:number
  successes:number
  failures:number
  input_tokens:number
  output_tokens:number
  total_tokens:number
  error_rate:number
}

/** One model's share of the member's own traffic, aggregated from the log. */
export type ConsoleUsageModel={
  alias:string
  requests:number
  input_tokens:number
  output_tokens:number
  total_tokens:number
}

/**
 * ConsoleUsageQuota is the Tenant's shared budget as a read-only aggregate.
 *
 * It contains no member or key detail: the page can say that the budget is
 * shared without revealing who else spent it. A band is null when the Tenant
 * has no configured limit for it — 未设, which must not render as 0%.
 */
export type ConsoleUsageQuota={
  tenant_id:string
  tenant_name:string
  daily_token:QuotaDimension|null
  monthly_token:QuotaDimension|null
  monthly_request:QuotaDimension|null
}

/**
 * ConsoleUsage is the personal usage page's payload.
 *
 * `cost_available` is false because the gateway records tokens, not prices. The
 * page renders that as an explicit note; it must never derive a currency
 * amount from the token counts above it.
 */
export type ConsoleUsage={
  from:string
  to:string
  bucket:'hour'|'day'
  totals:ConsoleUsageTotals
  series:TrendBucket[]
  by_model:ConsoleUsageModel[]
  tenant_quota:ConsoleUsageQuota|null
  cost_available:boolean
}

/**
 * fetchConsoleUsage reads the signed-in Account's own usage for a period.
 *
 * Owner and Tenant are not parameters: the gateway binds the owner from the
 * session and reads the Tenant from the Account's membership. The query string
 * only chooses the window, exactly as the administrator trend endpoint does.
 */
export async function fetchConsoleUsage(from:Date,to:Date,bucket:'hour'|'day'){
  const q=new URLSearchParams({
    from:from.toISOString(),
    to:to.toISOString(),
    bucket,
    tz_offset:String(localOffsetMinutes()),
  })
  return api<ConsoleUsage>(`/api/console/usage?${q}`,{},false)
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
 * AccountChangeSet reports which fields a patch actually moved.
 *
 * It is computed by the gateway from the before and after states, not from the
 * request body, so a patch that resent the current values reports no change and
 * the console can avoid claiming otherwise.
 */
export type AccountChangeSet={
  roles:boolean
  status:boolean
  display_name:boolean
  authentication:boolean
}

/**
 * updateUser sends only the fields that actually changed.
 *
 * A field that is absent from the patch is left alone by the gateway, so
 * omitting the ones the operator did not touch is what keeps a display-name
 * edit from signing the account out: sending all three fields would make every
 * save look like a role change.
 */
export async function updateUser(id:string,patch:UserPatch){
  const d=await api<{data:AccountUser;changes:AccountChangeSet}>(`/admin/users/${encodeURIComponent(id)}`,{
    method:'PATCH',
    body:JSON.stringify(patch),
  })
  return {user:d.data,changes:d.changes}
}

/**
 * assignUserTenant sets an Account's Tenant Membership, or clears it when the
 * id is empty.
 *
 * The gateway refuses with 409 while the Account owns any API Key, because a
 * key must not point at a Tenant its owner has left. The count is on the
 * error's `owned_key_count` field so the caller can say how many.
 */
export async function assignUserTenant(id:string,tenantId:string){
  const d=await api<{data:AccountUser}>(`/admin/users/${encodeURIComponent(id)}/tenant`,{
    method:'PUT',
    body:JSON.stringify({tenant_id:tenantId}),
  })
  return d.data
}

/**
 * listModelTestOptions reads the model picker for the connectivity test. It is
 * available to super_admin, gateway_admin and operator, unlike /admin/models.
 */
export async function listModelTestOptions(){
  const d=await api<{data:ModelTestOption[]}>('/admin/model-test-options')
  return d.data||[]
}

// --- traffic telemetry -----------------------------------------------------

/**
 * localOffsetMinutes is this browser's UTC offset in minutes east of UTC.
 *
 * The server cannot know it, and it is the only thing that makes a "day" on the
 * trend mean the reader's day rather than UTC's. Note the sign: getTimezoneOffset
 * returns minutes *west* of UTC, so UTC+8 reports -480 and this negates it.
 */
export function localOffsetMinutes(){
  return -new Date().getTimezoneOffset()
}

/**
 * fetchTrend reads the dashboard's traffic series for a window.
 *
 * Both ends are converted to RFC 3339 instants before they leave the browser.
 * The picker hands back a zoneless local string, and sending that would make the
 * server guess the zone — the one guess that silently shifts a window by hours
 * without anything failing.
 */
export async function fetchTrend(from:Date,to:Date,bucket:'hour'|'day'){
  const q=new URLSearchParams({
    from:from.toISOString(),
    to:to.toISOString(),
    bucket,
    tz_offset:String(localOffsetMinutes()),
  })
  return api<TrendSeries>(`/admin/status/trend?${q}`)
}

/**
 * summarizeRequestLogs aggregates the same rows the log table is filtered to.
 *
 * It takes the identical query string as the table on purpose: the summary sits
 * directly above the list, so the two describing different sets of rows would be
 * worse than having no summary at all.
 */
export async function summarizeRequestLogs(query:string){
  return api<RequestLogSummary>(`/admin/request-logs/summary?${query}`)
}

/**
 * fetchRequestLogUsage reads the usage-and-quota aggregate for a filter.
 *
 * It takes the identical query string as the log table and the summary, for the
 * same reason the summary does: the section it feeds sits directly above that
 * table, so a total, a quota share or a per-key breakdown computed over some
 * other selection would be worse than showing nothing at all.
 */
export async function fetchRequestLogUsage(query:string){
  return api<RequestLogUsage>(`/admin/request-logs/usage?${query}`)
}

/**
 * revokeApiKey permanently destroys a key's credential.
 *
 * DELETE is a revocation, not a row deletion: the gateway keeps the key so the
 * request log can still resolve it, disables it, and replaces its stored digest
 * with one no client holds. The key cannot be brought back by re-enabling it —
 * only a new key will work.
 */
export async function revokeApiKey(id:string){
  return api<{ok:boolean;id:string;status:number}>(`/admin/api-keys/${encodeURIComponent(id)}`,{method:'DELETE'})
}

/**
 * deleteApiKey removes the key row itself.
 *
 * This is the harder of the two irreversible operations: revocation keeps the
 * row so a request log can still name the key, deletion removes it, and a
 * historical request keeps only the id. `purge=1` is what tells the gateway
 * which one the caller means.
 */
export async function deleteApiKey(id:string){
  return api<{ok:boolean;id:string;deleted:boolean}>(`/admin/api-keys/${encodeURIComponent(id)}?purge=1`,{method:'DELETE'})
}

/**
 * deleteTenant removes a tenant and the API Keys issued under it.
 *
 * The gateway runs both deletes in one transaction because the keys must go
 * first (their tenant_id is a required foreign key). `deleted_keys` is the
 * count it actually removed, which the console reports back so the operator
 * sees what the confirmation touched.
 */
export async function deleteTenant(id:string){
  return api<{ok:boolean;id:string;deleted_keys:number}>(`/admin/tenants/${encodeURIComponent(id)}`,{method:'DELETE'})
}

export type TenantFacet={id:string;code:string;name:string}
export type APIKeyFacet={id:string;name:string;key_prefix:string;tenant_id:string}
export type RequestLogFacets={tenants:TenantFacet[];keys:APIKeyFacet[]}

/**
 * listRequestLogFacets reads the values the log filters can take.
 *
 * It comes from the log rather than from the tenant and key APIs, so a role that
 * may read logs can also filter them — and so the lists only offer tenants and
 * keys that have actually served traffic.
 */
export async function listRequestLogFacets(){
  return api<RequestLogFacets>('/admin/request-logs/facets')
}

// --- personal request records ---------------------------------------------

/**
 * ConsoleRequestLog is one Request Record owned by the signed-in Account.
 *
 * It is deliberately narrower than the administrator's RequestLog: there is no
 * client_ip, tenant_id, provider, upstream_model or route_priority field,
 * because the member endpoint does not send them. `api_key_id` is the
 * identifier the key filter uses and remains valid after the key is deleted.
 */
export type ConsoleRequestLog={
  request_id:string
  api_key_id:string
  requested_model:string
  actual_model:string
  input_tokens:number
  output_tokens:number
  total_tokens:number
  latency_ms:number
  status_code:number
  error_code:string
  failover:boolean
  attempt_count:number
  created_at:string
}

/** One record plus the Routing Attempt chain, fetched only when the drawer opens. */
export type ConsoleRequestLogDetail=ConsoleRequestLog&{attempt_chain:RequestAttempt[]}

export type ConsoleRequestLogPage={
  data:ConsoleRequestLog[]
  limit:number
  offset:number
}

/**
 * listConsoleRequestLogs reads the caller's own records.
 *
 * The query may carry model, status, api_key_id, from, to, limit and offset. It
 * cannot name an owner or a tenant: the gateway binds the session's account id
 * into the query and has no field for a client-supplied one.
 */
export async function listConsoleRequestLogs(query:string){
  const suffix=query?'?'+query:''
  return api<ConsoleRequestLogPage>(`/api/console/request-logs${suffix}`)
}

/** getConsoleRequestLog reads one record and its attempt chain; not-owned is a 404. */
export async function getConsoleRequestLog(id:string){
  const d=await api<{data:ConsoleRequestLogDetail}>(`/api/console/request-logs/${encodeURIComponent(id)}`)
  return d.data
}
