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

const TOKEN_KEY='campus_admin_token'
export function getToken(){return localStorage.getItem(TOKEN_KEY)||''}
export function setToken(v:string){if(v)localStorage.setItem(TOKEN_KEY,v);else localStorage.removeItem(TOKEN_KEY)}

export async function api<T>(path:string,opt:RequestInit={}):Promise<T>{
  const headers=new Headers(opt.headers||{})
  const token=getToken()
  if(token)headers.set('Authorization','Bearer '+token)
  if(opt.body && !headers.has('Content-Type'))headers.set('Content-Type','application/json')
  const r=await fetch(path,{...opt,headers,credentials:'same-origin'})
  const txt=await r.text()
  let data:any={}
  try{data=txt?JSON.parse(txt):{}}catch{data={raw:txt}}
  if(!r.ok)throw new Error(data?.error?.message||('HTTP '+r.status))
  return data as T
}

export async function download(path:string,filename:string){
  const headers=new Headers()
  const token=getToken()
  if(token)headers.set('Authorization','Bearer '+token)
  const r=await fetch(path,{headers,credentials:'same-origin'})
  if(!r.ok){let msg='HTTP '+r.status;try{const d=await r.json();msg=d?.error?.message||msg}catch{}throw new Error(msg)}
  const blob=await r.blob();const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=filename;a.click();URL.revokeObjectURL(url)
}
