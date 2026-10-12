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
export type WhoAmI={user:string;roles:string[];auth_mode:string}
export type AuthConfig={auth_mode:string;session_enabled:boolean;needs_setup:boolean;min_password_length:number}
export type LoginResult={token:string;user:string;roles:string[];expires_at:string}

const TOKEN_KEY='campus_admin_token'
export function getToken(){return localStorage.getItem(TOKEN_KEY)||''}
export function setToken(v:string){if(v)localStorage.setItem(TOKEN_KEY,v);else localStorage.removeItem(TOKEN_KEY)}

// onUnauthorized lets the shell drop back to the login screen the moment any
// request reports an expired or revoked session, instead of leaving the user
// staring at a page of failed panels.
let unauthorizedHandler:(()=>void)|null=null
export function onUnauthorized(fn:()=>void){unauthorizedHandler=fn}

const isAuthEndpoint=(path:string)=>path.startsWith('/admin/login')||path.startsWith('/admin/setup')||path.startsWith('/admin/auth-config')

export async function api<T>(path:string,opt:RequestInit={}):Promise<T>{
  const headers=new Headers(opt.headers||{})
  const token=getToken()
  if(token)headers.set('Authorization','Bearer '+token)
  if(opt.body && !headers.has('Content-Type'))headers.set('Content-Type','application/json')
  const r=await fetch(path,{...opt,headers,credentials:'same-origin'})
  const txt=await r.text()
  let data:any={}
  try{data=txt?JSON.parse(txt):{}}catch{data={raw:txt}}
  if(!r.ok){
    if(r.status===401 && !isAuthEndpoint(path)){setToken('');unauthorizedHandler?.()}
    throw new Error(data?.error?.message||('HTTP '+r.status))
  }
  return data as T
}

export async function getAuthConfig(){return api<AuthConfig>('/admin/auth-config')}

export async function login(username:string,password:string){
  return api<LoginResult>('/admin/login',{method:'POST',body:JSON.stringify({username,password})})
}

export async function setupAdmin(username:string,password:string,displayName:string,setupToken:string){
  return api<LoginResult>('/admin/setup',{method:'POST',body:JSON.stringify({username,password,display_name:displayName,setup_token:setupToken})})
}

export async function logout(){
  try{await api('/admin/logout',{method:'POST'})}finally{setToken('')}
}

export async function changePassword(oldPassword:string,newPassword:string){
  return api<{ok:boolean}>('/admin/password',{method:'POST',body:JSON.stringify({old_password:oldPassword,new_password:newPassword})})
}

export async function download(path:string,filename:string){
  const headers=new Headers()
  const token=getToken()
  if(token)headers.set('Authorization','Bearer '+token)
  const r=await fetch(path,{headers,credentials:'same-origin'})
  if(r.status===401&&!isAuthEndpoint(path)){setToken('');unauthorizedHandler?.()}
  if(!r.ok){let msg='HTTP '+r.status;try{const d=await r.json();msg=d?.error?.message||msg}catch{}throw new Error(msg)}
  const blob=await r.blob();const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=filename;a.click();URL.revokeObjectURL(url)
}
