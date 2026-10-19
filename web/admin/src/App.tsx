import {useEffect,useRef,useState,type FormEvent} from 'react'
import {
  Activity, AlertTriangle, BarChart3, Boxes, ChevronRight, CircleGauge, Database,
  Eye, EyeOff, FileClock, KeyRound, LayoutDashboard, Loader2, Lock, LogOut, Menu, Moon, Network,
  RefreshCw, Route, ServerCog, Settings, ShieldAlert, ShieldCheck, Sun, UserCog,
  UserRound, Users, X, Zap
} from 'lucide-react'
import {
  api, changePassword, clearSession, download, getAuthConfig, isSessionMode, listSessions, login, logout,
  refreshSession, revokeSession, setAuthMode, setUnauthenticatedHandler, setupAdmin, whoami,
  ADMIN_ROLES, createAdminUser, listAdminUsers, roleLabel, updateAdminUser, setAdminUserScope,
  TENANT_KINDS, tenantKindLabel, getTenantAllocation, allocateTenantQuota, moveTenant,
  listModelQuotas, upsertModelQuota, deleteModelQuota,
  type AdminAccount, type AdminAudit, type AdminSessionView, type ApiKey, type AuthConfig,
  type DashboardStatus, type EffectiveLimits, type EffectiveModelQuota, type ModelConfig,
  type ModelQuota, type ModelQuotaAllocation, type ModelRoute, type Provider,
  type RequestLog, type SecurityEvent, type Tenant, type TenantAllocation, type TenantQuotaValue,
  type TenantUsage, type WhoAmI
} from './api'
import {F,NumInput,QuotaFields,quotaLabel} from './fields'

type Page='dashboard'|'providers'|'models'|'tenants'|'modelQuotas'|'keys'|'logs'|'audit'|'security'|'admins'|'settings'
type Drawer={kind:string;data?:any}|null
type Toast={text:string;bad?:boolean}|null

// loading  — checking the auth config and trying to restore a session
// setup    — no administrator exists yet, show the first-run form
// login    — credentials required (session mode)
// ready    — signed in
// identity — trusted-header mode: the campus proxy must inject an identity
// unsupported — the gateway is running a legacy auth mode this UI cannot drive
type AuthPhase='loading'|'login'|'setup'|'ready'|'identity'|'unsupported'

// authModeOf normalises the mode string once so every branch agrees on it.
const authModeOf=(cfg:AuthConfig|null)=>(cfg?.auth_mode||'').toLowerCase()

const pages:{id:Page;label:string;icon:any;roles?:string[]}[]=[
  {id:'dashboard',label:'总览',icon:LayoutDashboard},
  {id:'providers',label:'供应商',icon:Network},
  {id:'models',label:'模型与路由',icon:Route},
  {id:'tenants',label:'租户',icon:Users},
  {id:'modelQuotas',label:'模型额度',icon:CircleGauge},
  {id:'keys',label:'API Key',icon:KeyRound},
  {id:'logs',label:'请求日志',icon:FileClock},
  {id:'audit',label:'管理审计',icon:Database},
  {id:'security',label:'安全事件',icon:ShieldAlert},
  {id:'admins',label:'管理员',icon:UserCog,roles:['super_admin']},
  {id:'settings',label:'系统设置',icon:Settings},
]

// visiblePages hides menu entries the signed-in account cannot use. This is
// presentation only: every request is still authorised server-side, so a
// hand-crafted call gets a 403 rather than data.
const visiblePages=(roles:string[])=>pages.filter(p=>!p.roles||p.roles.some(r=>roles.includes(r)))

type ProviderTemplate={id:string;label:string;values:{name:string;base_url:string;protocol:string;credential_env:string}}

// Data-driven supplier presets. They only prefill the form; every field stays
// editable and no real vendor API key is ever collected or stored.
const providerTemplates:ProviderTemplate[]=[
  {id:'custom',label:'自定义 / OpenAI Compatible',values:{name:'',base_url:'',protocol:'openai-chat',credential_env:''}},
  {id:'qwen',label:'阿里云百炼 / 通义千问',values:{name:'qwen',base_url:'https://dashscope.aliyuncs.com/compatible-mode/v1',protocol:'openai-chat',credential_env:'DASHSCOPE_API_KEY'}},
  {id:'deepseek',label:'DeepSeek',values:{name:'deepseek',base_url:'https://api.deepseek.com/v1',protocol:'openai-chat',credential_env:'DEEPSEEK_API_KEY'}},
  {id:'openai',label:'OpenAI',values:{name:'openai',base_url:'https://api.openai.com/v1',protocol:'openai-chat',credential_env:'OPENAI_API_KEY'}},
  {id:'glm',label:'智谱 GLM',values:{name:'glm',base_url:'https://open.bigmodel.cn/api/paas/v4',protocol:'openai-chat',credential_env:'ZHIPU_API_KEY'}},
  {id:'kimi',label:'Moonshot / Kimi',values:{name:'kimi',base_url:'https://api.moonshot.cn/v1',protocol:'openai-chat',credential_env:'MOONSHOT_API_KEY'}},
  {id:'doubao',label:'火山方舟 / 豆包',values:{name:'doubao',base_url:'https://ark.cn-beijing.volces.com/api/v3',protocol:'openai-chat',credential_env:'ARK_API_KEY'}},
]

const fmt=(n:number)=>new Intl.NumberFormat('zh-CN',{notation:n>=10000?'compact':'standard',maximumFractionDigits:1}).format(n||0)
const when=(v?:string)=>v?new Date(v).toLocaleString('zh-CN'):'—'
const ms=(n:number)=>n<1000?Math.round(n)+' ms':(n/1000).toFixed(1)+' s'
const status=(n:number)=><span className={'badge '+(n===1?'ok':'off')}>{n===1?'启用':'停用'}</span>
const unknown=(v:any)=>v===0||v===false?String(v):(v||<span className="unknown">未知</span>)

function App(){
  const [phase,setPhase]=useState<AuthPhase>('loading')
  const [authConfig,setAuthConfig]=useState<AuthConfig|null>(null)
  const [authNotice,setAuthNotice]=useState('')
  const [page,setPage]=useState<Page>('dashboard')
  const [collapsed,setCollapsed]=useState(()=>localStorage.getItem('campus_sidebar')==='1')
  const [dark,setDark]=useState(()=>localStorage.getItem('campus_theme')==='dark')
  const [drawer,setDrawer]=useState<Drawer>(null)
  const [toast,setToast]=useState<Toast>(null)
  const [busy,setBusy]=useState(false)
  const [statusData,setStatusData]=useState<DashboardStatus|null>(null)
  const [who,setWho]=useState<WhoAmI|null>(null)
  const [providers,setProviders]=useState<Provider[]>([])
  const [models,setModels]=useState<ModelConfig[]>([])
  const [tenants,setTenants]=useState<Tenant[]>([])
  const [modelQuotas,setModelQuotas]=useState<ModelQuota[]>([])
  const [keys,setKeys]=useState<ApiKey[]>([])
  const [routes,setRoutes]=useState<Record<string,ModelRoute[]>>({})
  const [logs,setLogs]=useState<RequestLog[]>([])
  const [audits,setAudits]=useState<AdminAudit[]>([])
  const [security,setSecurity]=useState<SecurityEvent[]>([])
  const [adminAccounts,setAdminAccounts]=useState<AdminAccount[]>([])
  const [logOffset,setLogOffset]=useState(0)
  const [filters,setFilters]=useState({model:'',provider:'',status:'',request_id:'',failover:''})

  useEffect(()=>{document.documentElement.dataset.theme=dark?'dark':'light';localStorage.setItem('campus_theme',dark?'dark':'light')},[dark])

  // Boot sequence: read the public auth config, then resolve the identity
  // appropriate to the active mode. In session mode that means restoring from
  // the HttpOnly refresh cookie; in trusted-header mode the campus reverse proxy
  // has already injected the identity, so /admin/whoami is the only call needed.
  // The access token is never restored from storage because it is never written
  // there.
  useEffect(()=>{
    let cancelled=false
    ;(async()=>{
      try{
        const cfg=await getAuthConfig()
        if(cancelled)return
        setAuthConfig(cfg)
        setAuthMode(authModeOf(cfg))
        const mode=authModeOf(cfg)

        if(mode==='trusted-header'){
          // No login form and no local session: ask the gateway who we are.
          try{
            const w=await whoami()
            if(cancelled)return
            setWho(w)
            setPhase('ready')
          }catch{
            if(!cancelled)setPhase('identity')
          }
          return
        }

        if(!cfg.login_enabled){setPhase('unsupported');return}
        if(cfg.needs_setup){setPhase('setup');return}
        const restored=await refreshSession()
        if(cancelled)return
        setPhase(restored?'ready':'login')
      }catch{
        if(!cancelled)setPhase('login')
      }
    })()
    return ()=>{cancelled=true}
  },[])

  // A 401 that survives one refresh attempt means the session is gone. What
  // that looks like depends on the mode: session mode returns to the login
  // screen, trusted-header mode can only report that the campus identity is
  // missing or lacks a management role.
  useEffect(()=>{
    setUnauthenticatedHandler(reason=>{
      setDrawer(null);setWho(null)
      if(authModeOf(authConfig)==='trusted-header'){
        setPhase('identity')
        return
      }
      setAuthNotice(reason==='revoked'?'会话已被撤销，请重新登录。':'登录状态已过期，请重新登录。')
      setPhase('login')
    })
  },[authConfig])

  useEffect(()=>{if(phase==='ready')loadPage(page)},[page,phase])
  useEffect(()=>{if(toast){const t=setTimeout(()=>setToast(null),2800);return()=>clearTimeout(t)}},[toast])

  const notify=(text:string,bad=false)=>setToast({text,bad})
  const run=async(fn:()=>Promise<void>)=>{setBusy(true);try{await fn()}catch(e:any){notify(e.message||String(e),true)}finally{setBusy(false)}}
  const list=async<T,>(path:string)=>((await api<{data:T[]}>(path)).data||[])

  function onAuthenticated(){setAuthNotice('');setPhase('ready');setPage('dashboard')}

  async function signOut(){
    await logout()
    setWho(null);setStatusData(null);setDrawer(null)
    setAuthNotice('已安全退出登录。')
    setPhase('login')
  }

  async function loadPage(p:Page){
    await run(async()=>{
      if(p==='dashboard'){
        const [s,w]=await Promise.all([api<DashboardStatus>('/admin/status'),api<WhoAmI>('/admin/whoami').catch(()=>null as any)])
        setStatusData(s);setWho(w)
      }
      if(p==='providers')setProviders(await list<Provider>('/admin/providers'))
      if(p==='models'){
        const [m,p]=await Promise.all([list<ModelConfig>('/admin/models'),list<Provider>('/admin/providers')])
        setModels(m);setProviders(p)
        const rs=await Promise.all(m.map(async x=>[x.id,await list<ModelRoute>('/admin/models/'+x.id+'/routes')] as const))
        setRoutes(Object.fromEntries(rs))
      }
      if(p==='tenants')setTenants(await list<Tenant>('/admin/tenants'))
      if(p==='modelQuotas'){
        // The tenant list feeds the "who holds this pool" picker, so both load
        // together rather than leaving the form with an empty dropdown.
        const [q,t,m]=await Promise.all([listModelQuotas(),list<Tenant>('/admin/tenants'),list<ModelConfig>('/admin/models')])
        setModelQuotas(q);setTenants(t);setModels(m)
      }
      if(p==='keys'){
        const [k,t,m]=await Promise.all([list<ApiKey>('/admin/api-keys'),list<Tenant>('/admin/tenants'),list<ModelConfig>('/admin/models')])
        setKeys(k);setTenants(t);setModels(m)
      }
      if(p==='logs')await loadLogs()
      if(p==='audit')setAudits(await list<AdminAudit>('/admin/admin-audit?limit=200'))
      if(p==='security')setSecurity(await list<SecurityEvent>('/admin/security-events?limit=200'))
      if(p==='admins'){
        // The scope picker needs the tenant tree, so load both together.
        const [accounts,t]=await Promise.all([listAdminUsers(),list<Tenant>('/admin/tenants')])
        setAdminAccounts(accounts);setTenants(t)
      }
      if(p==='settings')setWho(await api<WhoAmI>('/admin/whoami'))
    })
  }

  async function loadLogs(reset=false,requestedOffset?:number){
    const offset=reset?0:(requestedOffset??logOffset);if(reset)setLogOffset(0)
    const q=new URLSearchParams({limit:'50',offset:String(offset)})
    Object.entries(filters).forEach(([k,v])=>v&&q.set(k,v))
    const d=await api<{data:RequestLog[]}>('/admin/request-logs?'+q)
    setLogs(d.data||[])
  }

  async function saveProvider(v:any){
    await api(v.id?'/admin/providers/'+v.id:'/admin/providers',{method:v.id?'PUT':'POST',body:JSON.stringify(v)})
    notify('供应商已保存');setDrawer(null);setProviders(await list<Provider>('/admin/providers'))
  }
  async function saveModel(v:any){
    await api(v.id?'/admin/models/'+v.id:'/admin/models',{method:v.id?'PUT':'POST',body:JSON.stringify(v)})
    notify('模型已保存');setDrawer(null);await loadPage('models')
  }
  async function saveRoute(model:ModelConfig,v:any){
    await api(v.id?'/admin/model-routes/'+v.id:'/admin/models/'+model.id+'/routes',{method:v.id?'PUT':'POST',body:JSON.stringify(v)})
    notify('线路已保存');setDrawer(null);await loadPage('models')
  }
  async function deleteRoute(v:ModelRoute){
    if(!confirm('确认删除线路 '+v.provider_name+' → '+v.upstream_model+'？'))return
    await api('/admin/model-routes/'+v.id,{method:'DELETE'});notify('线路已删除');await loadPage('models')
  }
  async function saveTenant(v:any){
    await api(v.id?'/admin/tenants/'+v.id:'/admin/tenants',{method:v.id?'PUT':'POST',body:JSON.stringify(v)})
    notify('租户已保存');setDrawer(null);setTenants(await list<Tenant>('/admin/tenants'))
  }
  async function saveModelQuota(v:ModelQuotaAllocation){
    await upsertModelQuota(v)
    notify('模型额度已保存');setDrawer(null);await loadPage('modelQuotas')
  }
  async function removeModelQuota(tenantId:string|null,alias:string){
    const who=tenantId?tenants.find(t=>t.id===tenantId)?.name||'该租户':'全校'
    if(!confirm(`确认删除「${who}」对模型 ${alias} 的额度？删除后该主体将改为继承上级额度。`))return
    await deleteModelQuota(tenantId,alias)
    notify('模型额度已删除');await loadPage('modelQuotas')
  }
  async function saveKey(v:any){
    const out:any=await api(v.id?'/admin/api-keys/'+v.id:'/admin/api-keys',{method:v.id?'PUT':'POST',body:JSON.stringify(v)})
    if(out.api_key)setDrawer({kind:'secret',data:out.api_key});else setDrawer(null)
    notify(v.id?'Key 已更新':'Key 已创建');setKeys(await list<ApiKey>('/admin/api-keys'))
  }
  async function syncThinkWatch(){
    if(!confirm('确认把当前供应商 / 模型 / 线路配置同步到 ThinkWatch？'))return
    const out:any=await api('/admin/thinkwatch/sync',{method:'POST'})
    notify('ThinkWatch 同步成功：'+out.providers+' 个供应商 / '+out.aliases+' 个对外模型')
    setTimeout(()=>loadPage('dashboard'),1200)
  }
  async function modelTest(model:string,prompt:string){
    const out:any=await api('/admin/model-test',{method:'POST',body:JSON.stringify({model,prompt})})
    setDrawer({kind:'testResult',data:out})
  }
  async function tenantUsage(t:Tenant){
    // The usage panel shows effective limits rather than the tenant's own
    // values: a tenant that inherits everything has null for all four, and
    // rendering "null / null" would tell the operator nothing about the budget
    // that is actually being enforced.
    const [u,view]=await Promise.all([
      api<TenantUsage>('/admin/tenants/'+t.id+'/usage'),
      getTenantAllocation(t.id),
    ])
    setDrawer({kind:'usage',data:{tenant:t,usage:u,effective:view.effective}})
  }
  // The allocation drawer loads the resolved view first: an operator about to
  // hand down a budget needs to see what the level above already holds, and
  // what this node currently inherits, before typing a number.
  async function tenantAllocation(t:Tenant){
    const view=await getTenantAllocation(t.id)
    setDrawer({kind:'allocate',data:{tenant:t,view}})
  }
  async function saveAllocation(id:string,input:TenantAllocation){
    const view=await allocateTenantQuota(id,input)
    notify('额度已分配')
    setDrawer({kind:'allocate',data:{tenant:view.tenant,view}})
    setTenants(await list<Tenant>('/admin/tenants'))
  }

  // The auth screens replace the whole shell. All hooks above run on every
  // render, so the early returns below are safe.
  if(phase==='loading')return <AuthSplash text="正在检查登录状态…"/>
  if(phase==='unsupported')return <AuthUnsupported config={authConfig}/>
  if(phase==='identity')return <IdentityRequired onRetry={()=>window.location.reload()}/>
  if(phase==='setup')return <SetupPage config={authConfig} onDone={onAuthenticated}/>
  if(phase==='login')return <LoginPage config={authConfig} notice={authNotice} onDone={onAuthenticated}/>

  const sessionMode=isSessionMode()

  const title=pages.find(x=>x.id===page)?.label||''
  return <div className="shell">
    <aside className={'sidebar '+(collapsed?'collapsed':'')}>
      <div className="brand">
        <div className="brandMark">AI</div>
        {!collapsed&&<div><b>校园模型网关</b><span>Campus AI Gateway</span></div>}
      </div>
      <nav>{visiblePages(who?.roles||[]).map(p=><button key={p.id} className={page===p.id?'active':''} onClick={()=>setPage(p.id)} title={p.label}><p.icon/><span>{p.label}</span></button>)}</nav>
      <button className="collapseBtn" onClick={()=>{const v=!collapsed;setCollapsed(v);localStorage.setItem('campus_sidebar',v?'1':'0')}}><Menu/></button>
    </aside>
    <section className="workspace">
      <header className="topbar">
        <div><h1>{title}</h1><p>{page==='dashboard'?'统一模型接入、路由、访问控制与审计':'校园 AI 基础设施管理中心'}</p></div>
        <div className="topActions">
          {busy&&<span className="loading"><RefreshCw/> 正在刷新</span>}
          {who&&<span className="identity">{who.username||who.user}<small>{who.roles?.join(' · ')}</small></span>}
          <button className="iconBtn" onClick={()=>setDark(!dark)} title={dark?'切换到浅色主题':'切换到深色主题'}>{dark?<Sun/>:<Moon/>}</button>
          {sessionMode&&<button className="iconBtn" onClick={()=>run(signOut)} title="退出登录"><LogOut/></button>}
        </div>
      </header>
      <main>
        {page==='dashboard'&&<Dashboard data={statusData} onRefresh={()=>loadPage('dashboard')}/>}
        {page==='providers'&&<Providers items={providers} onAdd={()=>setDrawer({kind:'provider',data:{status:1,protocol:'openai-chat'}})} onEdit={v=>setDrawer({kind:'provider',data:v})}/>}
        {page==='models'&&<Models items={models} routes={routes} onAdd={()=>setDrawer({kind:'model',data:{status:1}})} onEdit={v=>setDrawer({kind:'model',data:v})} onRoute={(m,r)=>setDrawer({kind:'route',data:{model:m,route:r||{priority:1,weight:100,timeout_ms:60000,status:1}}})} onDeleteRoute={v=>run(()=>deleteRoute(v))} onSync={()=>run(syncThinkWatch)} onTest={()=>setDrawer({kind:'test',data:{model:models.find(x=>x.status===1)?.alias||''}})}/>}
        {page==='tenants'&&<Tenants items={tenants} onAdd={()=>setDrawer({kind:'tenant',data:{status:1,kind:'tenant',daily_token_limit:null,monthly_token_limit:null,monthly_request_limit:null,monthly_budget_micros:null}})} onAddChild={v=>setDrawer({kind:'tenant',data:{status:1,kind:'student',parent_id:v.id,daily_token_limit:null,monthly_token_limit:null,monthly_request_limit:null,monthly_budget_micros:null}})} onEdit={v=>setDrawer({kind:'tenant',data:v})} onUsage={v=>run(()=>tenantUsage(v))} onAllocate={v=>run(()=>tenantAllocation(v))}/>}
        {page==='modelQuotas'&&<ModelQuotas items={modelQuotas} tenants={tenants} models={models} onAdd={()=>setDrawer({kind:'modelQuota',data:{tenant_id:null,model_alias:'',daily_token_limit:null,monthly_token_limit:null}})} onEdit={v=>setDrawer({kind:'modelQuota',data:v})} onDelete={(t,a)=>run(()=>removeModelQuota(t,a))}/>}
        {page==='keys'&&<Keys items={keys} onAdd={()=>setDrawer({kind:'key',data:{status:1,rpm_limit:60,tpm_limit:100000,max_concurrency:10,allowed_models:[],allowed_ips:[]}})} onEdit={v=>setDrawer({kind:'key',data:v})}/>}
        {page==='logs'&&<Logs rows={logs} filters={filters} setFilters={setFilters} offset={logOffset} onApply={()=>run(()=>loadLogs(true))} onPrev={()=>{const n=Math.max(0,logOffset-50);setLogOffset(n);run(()=>loadLogs(false,n))}} onNext={()=>{const n=logOffset+50;setLogOffset(n);run(()=>loadLogs(false,n))}} onDetail={v=>setDrawer({kind:'log',data:v})} onExport={()=>run(()=>download('/admin/request-logs/export?'+new URLSearchParams(Object.entries(filters).filter(([,v])=>v) as [string,string][]),'campus-gateway-request-logs.csv'))}/>}
        {page==='audit'&&<Audit rows={audits}/>}
        {page==='security'&&<Security rows={security} onDetail={v=>setDrawer({kind:'security',data:v})}/>}
        {page==='admins'&&<AdminsPage items={adminAccounts} tenants={tenants} currentUsername={who?.username||who?.user||''} onRefresh={()=>loadPage('admins')} onNotice={notify}/>}
        {page==='settings'&&<AccountPage who={who} onNotice={notify} onSignedOut={signOut}/>}
      </main>
    </section>
    {drawer&&<DrawerView drawer={drawer} providers={providers} tenants={tenants} models={models} onClose={()=>setDrawer(null)} onProvider={v=>run(()=>saveProvider(v))} onModel={v=>run(()=>saveModel(v))} onRoute={(m,v)=>run(()=>saveRoute(m,v))} onTenant={v=>run(()=>saveTenant(v))} onAllocate={(id,v)=>run(()=>saveAllocation(id,v))} onModelQuota={v=>run(()=>saveModelQuota(v))} onKey={v=>run(()=>saveKey(v))} onTest={(m,p)=>run(()=>modelTest(m,p))}/>}
    {toast&&<div className={'toast '+(toast.bad?'bad':'')}>{toast.bad?<AlertTriangle/>:<Zap/>}{toast.text}</div>}
  </div>
}

function Dashboard({data,onRefresh}:{data:DashboardStatus|null,onRefresh:()=>void}){
  const s=data?.stats
  const health=[['Gateway',data?.gateway,ServerCog],['PostgreSQL',data?.postgres,Database],['Redis',data?.redis,Boxes],['ThinkWatch',data?.thinkwatch,Network]] as const
  return <div className="stack">
    <section className={'hero '+(health.every(x=>x[1])?'healthy':'warning')}>
      <div className="heroIcon"><CircleGauge/></div><div><h2>{health.every(x=>x[1])?'系统运行正常':'检测到组件异常'}</h2><p>Gateway 控制面与模型数据面实时状态</p></div><button className="btn secondary" onClick={onRefresh}><RefreshCw/>刷新</button>
    </section>
    <div className="healthGrid">{health.map(([n,ok,I])=><div className="healthCard" key={n}><I/><div><b>{n}</b><span><i className={ok?'green':'red'}/>{ok?'运行正常':'不可用'}</span></div></div>)}</div>
    <div className="metricGrid">
      <Metric label="今日请求" value={fmt(s?.today_requests||0)} icon={Activity}/>
      <Metric label="今日 Tokens" value={fmt(s?.today_tokens||0)} icon={Zap}/>
      <Metric label="今日错误" value={fmt(s?.today_errors||0)} icon={AlertTriangle}/>
      <Metric label="平均延迟" value={ms(s?.avg_latency_ms||0)} icon={BarChart3}/>
      <Metric label="P95 延迟" value={ms(s?.p95_latency_ms||0)} icon={CircleGauge}/>
      <Metric label="故障切换" value={fmt(s?.failover_count||0)} icon={Route}/>
    </div>
    <Panel title="供应商健康概览" sub="实际供应商指标依赖线路遥测；未知数据不会被猜测。">
      {(data?.provider_stats||[]).length?<div className="providerGrid">{data!.provider_stats.map(v=><div className="providerStat" key={v.provider}><b>{v.provider}</b><strong>{v.success_rate.toFixed(1)}%</strong><span>{v.requests} 请求 · {ms(v.avg_latency_ms)}</span></div>)}</div>:<Empty text="暂无可归因的供应商遥测数据"/>}
    </Panel>
  </div>
}
function Metric({label,value,icon:I}:{label:string;value:string;icon:any}){return <div className="metricCard"><span><I/>{label}</span><strong>{value}</strong></div>}
function Panel({title,sub,actions,children}:{title:string;sub?:string;actions?:any;children:any}){return <section className="panel"><div className="panelHead"><div><h2>{title}</h2>{sub&&<p>{sub}</p>}</div>{actions}</div>{children}</section>}
function Empty({text}:{text:string}){return <div className="empty"><Boxes/><b>{text}</b><span>当前没有可展示的数据</span></div>}

function Providers({items,onAdd,onEdit}:{items:Provider[];onAdd:()=>void;onEdit:(v:Provider)=>void}){
 return <Panel title="供应商管理" sub="只保存凭据环境变量名，不保存真实厂商 API Key。" actions={<button className="btn primary" onClick={onAdd}>新增供应商</button>}>
  {items.length?<Table heads={['供应商','Base URL','接口协议','凭据环境变量','状态','操作']}>{items.map(v=><tr key={v.id}><td><b>{v.name}</b></td><td className="mono cut">{v.base_url}</td><td><span className="badge">{v.protocol}</span></td><td className="mono">{v.credential_env}</td><td>{status(v.status)}</td><td><button className="link" onClick={()=>onEdit(v)}>编辑</button></td></tr>)}</Table>:<Empty text="尚未配置供应商"/>}
 </Panel>
}

function Models({items,routes,onAdd,onEdit,onRoute,onDeleteRoute,onSync,onTest}:{items:ModelConfig[];routes:Record<string,ModelRoute[]>;onAdd:()=>void;onEdit:(v:ModelConfig)=>void;onRoute:(m:ModelConfig,r?:ModelRoute)=>void;onDeleteRoute:(r:ModelRoute)=>void;onSync:()=>void;onTest:()=>void}){
 return <div className="stack">
  <Panel title="模型与路由" sub="业务系统只使用统一的对外模型名称，实际供应商和上游模型可在这里配置并进行故障切换。" actions={<><button className="btn secondary" onClick={onTest}>测试模型</button><button className="btn secondary" onClick={onSync}>同步 ThinkWatch</button><button className="btn primary" onClick={onAdd}>新增模型</button></>}>
   <div className="modelCards">{items.map(m=>{const rs=(routes[m.id]||[]).slice().sort((a,b)=>a.priority-b.priority||b.weight-a.weight);const primaryIdx=rs.findIndex(r=>r.status===1);return <article className="modelCard" key={m.id}>
    <div className="modelTitle"><div><span className="eyebrow">对外模型</span><h3>{m.alias}</h3><p className="muted">API 调用名</p></div><div>{status(m.status)} <button className="link" onClick={()=>onEdit(m)}>编辑</button></div></div>
    <div className="routeFlow">
      {rs.map((r,i)=><div key={r.id} className={'routeStep '+(r.status===1?'':'disabled')}>
        {i>0&&<div className="failLine"><span>故障切换</span><ChevronRight/></div>}
        <div className="routeBox">
          <div><b>{r.provider_name}</b><span>{status(r.status)}<span className="badge">{i===primaryIdx?'主线路':'备用线路'}</span></span></div><strong>{r.upstream_model}</strong>
          <p>优先级 {r.priority} · 权重 {r.weight} · 超时 {r.timeout_ms} ms</p>
          <div><button className="link" onClick={()=>onRoute(m,r)}>编辑</button><button className="link dangerText" onClick={()=>onDeleteRoute(r)}>删除</button></div>
        </div>
      </div>)}
      {!rs.length&&<Empty text="该模型尚未配置线路"/>}
    </div>
    <button className="addRoute" onClick={()=>onRoute(m)}>+ 新增备用线路</button>
   </article>})}</div>
  </Panel>
 </div>
}

function Tenants({items,onAdd,onAddChild,onEdit,onUsage,onAllocate}:{items:Tenant[];onAdd:()=>void;onAddChild:(v:Tenant)=>void;onEdit:(v:Tenant)=>void;onUsage:(v:Tenant)=>void;onAllocate:(v:Tenant)=>void}){
 return <Panel title="租户与分级分配" sub="租户是一棵树：学校 → 院系 → 教师 → 学生。上级给下级分配额度，请求会同时计入链上每一级。" actions={<button className="btn primary" onClick={onAdd}>新增根租户</button>}>
  {items.length?<div className="tenantGrid">{items.map(v=><article className={'tenantCard depth-'+Math.min(v.depth||0,4)} key={v.id}>
   <div className="tenantHead">
    {/* depth 直接来自物化路径，前端不再自己推导层级 */}
    {(v.depth||0)>0&&<span className="treeMark" aria-hidden>└</span>}
    <div><span className="eyebrow">{v.code} · {tenantKindLabel(v.kind)}</span><h3>{v.name}</h3></div>
   </div>
   {status(v.status)}
   <div className="quotaRows"><Quota label="日 Token" n={v.daily_token_limit}/><Quota label="月 Token" n={v.monthly_token_limit}/><Quota label="月请求" n={v.monthly_request_limit}/></div>
   <div className="cardActions">
    <button className="btn secondary" onClick={()=>onAllocate(v)}>分配额度</button>
    <button className="btn secondary" onClick={()=>onUsage(v)}>查看用量</button>
    <button className="btn ghost" onClick={()=>onAddChild(v)}>建下级</button>
    <button className="btn ghost" onClick={()=>onEdit(v)}>编辑</button>
   </div>
  </article>)}</div>:<Empty text="尚未创建租户"/>}
 </Panel>
}

// Quota renders a tri-state value. null means the tenant inherits from the
// nearest ancestor that sets one, which must not be shown as "0" or "不限" --
// those are different states and the operator has to be able to tell them apart.
function Quota({label,n}:{label:string;n:number|null|undefined}){
 const text=quotaLabel(n,fmt)
 return <div><span>{label}</span><b className={n===null||n===undefined?'inherited':''}>{text}</b></div>
}

// QuotaText is Quota without the label, for table cells.
function QuotaText({n}:{n:TenantQuotaValue}){
 return <b className={n===null||n===undefined?'inherited':''}>{quotaLabel(n,fmt)}</b>
}

// ModelQuotas manages the per-model pools. One pool per model, held either by
// the whole campus (the shared pool, which is the root every tenant inherits
// from) or by a single tenant.
//
// The pools are deliberately not merged into one total. The unit is tokens, and
// a token of one model is not a token of another, so a combined figure would be
// a sum of incomparable quantities -- it could neither describe cost nor guide
// allocation.
function ModelQuotas({items,tenants,models,onAdd,onEdit,onDelete}:{items:ModelQuota[];tenants:Tenant[];models:ModelConfig[];onAdd:()=>void;onEdit:(v:ModelQuota)=>void;onDelete:(tenantId:string|null,alias:string)=>void}){
 // Shared pools first: they are the ceiling every tenant inherits from, so
 // reading the table top-down matches the order the chain resolves in.
 const shared=items.filter(v=>v.tenant_id===null)
 const perTenant=items.filter(v=>v.tenant_id!==null)
 const rows=[...shared,...perTenant]
 return <div className="stack">
  <Panel title="模型额度池" sub="每个模型一个独立的池：先给模型设一个全校共享的上限，再按租户逐级收窄。请求会同时计入模型链和租户链，任一超限即拒绝。" actions={<button className="btn primary" onClick={onAdd}>新增模型额度</button>}>
   {rows.length?<Table heads={['持有主体','模型','每日 Token','每月 Token','']}>
    {rows.map(v=><tr key={(v.tenant_id||'shared')+':'+v.model_alias}>
     <td>{v.tenant_id?<b>{v.tenant_name||v.tenant_code}</b>:<span className="badge ok">全校共享池</span>}</td>
     <td className="mono">{v.model_alias}</td>
     <td><QuotaText n={v.daily_token_limit}/></td>
     <td><QuotaText n={v.monthly_token_limit}/></td>
     <td className="rowActions"><button className="link" onClick={()=>onEdit(v)}>编辑</button><button className="link dangerText" onClick={()=>onDelete(v.tenant_id,v.model_alias)}>删除</button></td>
    </tr>)}
   </Table>:<Empty text="尚未配置任何模型额度"/>}
  </Panel>
  <Panel title="怎么理解这些池子" sub="三点容易踩空的地方。">
   <ul className="hintList">
    <li><b>全校共享池是根。</b>它被用满时，全校对该模型的调用都会被拒绝，不论各租户自己还剩多少。</li>
    <li><b>租户设 0（不限额）不能突破共享池。</b>0 只是取消该租户自己的那一层上限，共享池是独立的一层，仍然生效。</li>
    <li><b>留空 = 继承。</b>子级不配就取最近配过的祖先；删除某行同样等于回到继承。</li>
   </ul>
  </Panel>
 </div>
}

function ModelQuotaForm({value,tenants,models,onSave}:{value:any;tenants:Tenant[];models:ModelConfig[];onSave:(v:ModelQuotaAllocation)=>void}){
 const [v,setV]=useState(()=>({
  tenant_id:value.tenant_id??null,
  model_alias:value.model_alias||'',
  daily_token_limit:value.daily_token_limit??null,
  monthly_token_limit:value.monthly_token_limit??null,
 }))
 // The subject and the model are the row's identity. Changing either would be a
 // create, not an edit, so on an existing row they are shown but locked --
 // silently writing a second row is worse than a moment's friction.
 const locked=!!value.model_alias
 const isShared=v.tenant_id===null
 const activeModels=models.filter(m=>m.status===1)
 return <Form onSubmit={()=>onSave({...v})}>
  <F label="持有主体" hint="「全校共享池」是所有租户共用的根；选具体租户则只约束该租户及其下级。">
   <select value={v.tenant_id||''} disabled={locked} onChange={e=>setV({...v,tenant_id:e.target.value||null})}>
    <option value="">全校共享池（所有租户共用）</option>
    {tenants.map(t=><option key={t.id} value={t.id}>{'　'.repeat(Math.min(t.depth||0,4))}{t.name}（{t.code}）</option>)}
   </select>
  </F>
  <F label="模型" hint="每个模型一个独立的池；不同模型的 token 不可比，不合并计算。">
   {locked?<input value={v.model_alias} disabled/>:
    activeModels.length?<select value={v.model_alias} onChange={e=>setV({...v,model_alias:e.target.value})}>
     <option value="">请选择模型</option>
     {activeModels.map(m=><option key={m.id} value={m.alias}>{m.alias}</option>)}
    </select>:<input value={v.model_alias} placeholder="模型别名" onChange={e=>setV({...v,model_alias:e.target.value})}/>}
  </F>
  <div className="formGrid">
   <F label="每日 Token 上限" hint="留空=继承 · 0=不限额"><NumInput value={v.daily_token_limit} onValue={n=>setV({...v,daily_token_limit:n})}/></F>
   <F label="每月 Token 上限" hint="留空=继承 · 0=不限额"><NumInput value={v.monthly_token_limit} onValue={n=>setV({...v,monthly_token_limit:n})}/></F>
  </div>
  {isShared&&<div className="notice"><ShieldCheck/>全校共享池是这条链的根：所有租户对该模型的用量都会同时计入它。它被用满时，全校对该模型的调用都会被拒绝。</div>}
  {!isShared&&<div className="notice"><ShieldCheck/>子级只能收窄，不能超过上级对同一模型持有的额度；配一个不在白名单内的模型会被直接拒绝。</div>}
 </Form>
}

function Keys({items,onAdd,onEdit}:{items:ApiKey[];onAdd:()=>void;onEdit:(v:ApiKey)=>void}){
 return <Panel title="Virtual API Key" sub="完整 Key 只在创建时显示一次，数据库仅保存前缀与 HMAC 摘要。" actions={<button className="btn primary" onClick={onAdd}>创建 Key</button>}>
  {items.length?<Table heads={['名称','租户','Key 前缀','允许模型','RPM / TPM / 并发','最后使用','状态','']}>{items.map(v=><tr key={v.id}><td><b>{v.name}</b></td><td>{v.tenant_name}</td><td className="mono">{v.key_prefix}</td><td>{(v.allowed_models||[]).map(x=><span className="badge" key={x}>{x}</span>)}</td><td>{v.rpm_limit} / {v.tpm_limit} / {v.max_concurrency}</td><td>{when(v.last_used_at)}</td><td>{status(v.status)}</td><td><button className="link" onClick={()=>onEdit(v)}>编辑</button></td></tr>)}</Table>:<Empty text="尚未创建 API Key"/>}
 </Panel>
}

function Logs({rows,filters,setFilters,offset,onApply,onPrev,onNext,onDetail,onExport}:{rows:RequestLog[];filters:any;setFilters:any;offset:number;onApply:()=>void;onPrev:()=>void;onNext:()=>void;onDetail:(v:RequestLog)=>void;onExport:()=>void}){
 const f=(k:string)=>(e:any)=>setFilters({...filters,[k]:e.target.value})
 return <div className="stack"><Panel title="请求日志" sub="一次请求对应一行。点击可查看线路 / 用量 / 时延详情。" actions={<button className="btn secondary" onClick={onExport}>导出 CSV</button>}>
  <div className="filters"><input placeholder="模型" value={filters.model} onChange={f('model')}/><input placeholder="供应商" value={filters.provider} onChange={f('provider')}/><input placeholder="HTTP 状态" value={filters.status} onChange={f('status')}/><input placeholder="Request ID" value={filters.request_id} onChange={f('request_id')}/><select value={filters.failover} onChange={f('failover')}><option value="">全部故障切换</option><option value="true">发生故障切换</option><option value="false">未发生故障切换</option></select><button className="btn primary" onClick={onApply}>筛选</button></div>
  {rows.length?<Table heads={['时间','Request ID','线路','Token','延迟','状态']}>{rows.map(v=><tr key={v.request_id} className="clickRow" onClick={()=>onDetail(v)}><td>{when(v.created_at)}</td><td className="mono">{v.request_id.slice(0,14)}…</td><td><b>{v.requested_model}</b><br/><span className="muted">{v.provider||'未知'} / {v.upstream_model||v.actual_model||'未知'}</span>{v.failover&&<span className="badge warn">故障切换 {v.attempts} 次</span>}</td><td>{v.total_tokens}</td><td>{ms(v.latency_ms)}</td><td><span className={'badge '+(v.status_code<400?'ok':'bad')}>{v.status_code}</span></td></tr>)}</Table>:<Empty text="没有匹配的请求日志"/>}
  <div className="pager"><button className="btn secondary" disabled={offset===0} onClick={onPrev}>上一页</button><span>Offset {offset} · 本页 {rows.length}</span><button className="btn secondary" disabled={rows.length<50} onClick={onNext}>下一页</button></div>
 </Panel></div>
}

function Audit({rows}:{rows:AdminAudit[]}){return <Panel title="管理审计" sub="记录管理员对控制面的修改操作。">{rows.length?<Table heads={['时间','用户','操作','资源','来源','状态']}>{rows.map(v=><tr key={v.id}><td>{when(v.created_at)}</td><td><b>{v.actor}</b></td><td><span className="badge">{v.action}</span></td><td className="mono">{v.resource}</td><td>{v.client_ip||'—'}</td><td>{v.status_code}</td></tr>)}</Table>:<Empty text="暂无管理审计记录"/>}</Panel>}
function Security({rows,onDetail}:{rows:SecurityEvent[];onDetail:(v:SecurityEvent)=>void}){return <Panel title="安全事件" sub="集中展示鉴权、权限、限流和租户配额拒绝事件。">{rows.length?<Table heads={['时间','事件','模型','来源 IP','详情']}>{rows.map(v=><tr key={v.id} className="clickRow" onClick={()=>onDetail(v)}><td>{when(v.created_at)}</td><td><span className="badge bad">{v.event_type}</span></td><td>{v.requested_model||'—'}</td><td>{v.client_ip||'—'}</td><td className="cut">{v.detail||'—'}</td></tr>)}</Table>:<Empty text="暂无安全事件"/>}</Panel>}

// ---------------------------------------------------------------------------
// Authentication screens
// ---------------------------------------------------------------------------

function AuthSplash({text}:{text:string}){
  return <div className="authPage"><div className="authCard authCenter">
    <div className="brandMark lg">AI</div>
    <p className="authHint"><Loader2 className="spin"/> {text}</p>
  </div></div>
}

// PasswordInput is a password field with a show/hide toggle.
//
// The toggle is an icon-only control, so it carries both an aria-label and a
// title: without them a screen reader announces nothing useful.
function PasswordInput({value,onChange,placeholder,autoComplete,autoFocus}:{
  value:string
  onChange:(v:string)=>void
  placeholder?:string
  autoComplete?:string
  autoFocus?:boolean
}){
  const [visible,setVisible]=useState(false)
  const label=visible?'隐藏密码':'显示密码'
  return <div className="passwordWrap">
    <input
      type={visible?'text':'password'}
      value={value}
      placeholder={placeholder}
      autoComplete={autoComplete}
      autoFocus={autoFocus}
      onChange={e=>onChange(e.target.value)}
    />
    <button type="button" className="revealBtn" onClick={()=>setVisible(v=>!v)}
      aria-label={label} title={label} tabIndex={-1}>
      {visible?<EyeOff/>:<Eye/>}
    </button>
  </div>
}

function AuthUnsupported({config}:{config:AuthConfig|null}){
  const mode=(config?.auth_mode||'').toLowerCase()
  const tokenMode=mode==='token'||mode===''
  return <div className="authPage"><div className="authCard authCenter">
    <div className="brandMark lg">AI</div>
    <h1 className="authTitle">{tokenMode?'当前为应急 Token 模式':'控制台登录未启用'}</h1>
    {tokenMode
      ? <p className="authHint">
          网关正以应急 Token 模式运行，管理控制台不提供登录入口。请通过管理 API 使用
          <code>ADMIN_TOKEN</code> 访问，或将 <code>ADMIN_AUTH_MODE</code> 切换为
          <code>session</code> 后使用账号密码登录。
        </p>
      : <p className="authHint">
          当前网关的认证模式为 <code>{config?.auth_mode||'未知'}</code>，
          管理控制台无法驱动该模式。请联系校园信息化管理部门确认网关配置。
        </p>}
  </div></div>
}

// IdentityRequired is the trusted-header failure screen. There is no local
// credential to enter: the campus reverse proxy injects the identity, so the
// only useful actions are to finish the campus login and retry.
function IdentityRequired({onRetry}:{onRetry:()=>void}){
  return <div className="authPage"><div className="authCard authCenter">
    <div className="brandMark lg">AI</div>
    <h1 className="authTitle">统一身份认证未完成</h1>
    <p className="authHint">
      当前网关使用学校统一身份认证。未能取得有效的管理身份，可能是统一身份认证尚未完成，
      或当前账号不具备管理权限。请完成校园统一身份认证后重试，或联系校园信息化管理部门。
    </p>
    <button className="btn primary authSubmit" onClick={onRetry}><RefreshCw/>重新检查</button>
  </div></div>
}

function LoginPage({config,notice,onDone}:{config:AuthConfig|null;notice:string;onDone:()=>void}){
  const [username,setUsername]=useState('')
  const [password,setPassword]=useState('')
  const [error,setError]=useState('')
  const [busy,setBusy]=useState(false)

  const submit=async(e:FormEvent)=>{
    e.preventDefault()
    if(busy)return
    setBusy(true);setError('')
    try{
      await login(username.trim(),password)
      onDone()
    }catch(err:any){
      setError(err?.message||'登录失败')
    }finally{
      setBusy(false)
    }
  }

  return <div className="authPage">
    <form className="authCard" onSubmit={submit}>
      <div className="authHead">
        <div className="brandMark lg">AI</div>
        <div><h1>校园模型网关</h1><p>Campus AI Gateway 管理控制台</p></div>
      </div>
      {notice&&<div className="authNotice"><ShieldCheck/>{notice}</div>}
      <label className="field"><span>用户名</span>
        <input autoFocus autoComplete="username" value={username} placeholder="请输入管理员用户名"
          onChange={e=>setUsername(e.target.value)}/>
      </label>
      <label className="field"><span>密码</span>
        <PasswordInput autoComplete="current-password" value={password} placeholder="请输入密码"
          onChange={setPassword}/>
      </label>
      {error&&<div className="authError"><AlertTriangle/>{error}</div>}
      <button className="btn primary authSubmit" type="submit" disabled={busy||!username.trim()||!password}>
        {busy?<><Loader2 className="spin"/>正在登录</>:<><Lock/>登录</>}
      </button>
      <p className="authHint">忘记密码请联系校园信息化管理部门重置管理员账号。</p>
      {config&&config.access_ttl_seconds>0&&<p className="authHint subtle">登录状态会安全保存并在后台自动续期，不会写入浏览器本地存储。</p>}
    </form>
  </div>
}

function SetupPage({config,onDone}:{config:AuthConfig|null;onDone:()=>void}){
  const [username,setUsername]=useState('admin')
  const [displayName,setDisplayName]=useState('')
  const [password,setPassword]=useState('')
  const [confirmPassword,setConfirmPassword]=useState('')
  const [error,setError]=useState('')
  const [busy,setBusy]=useState(false)

  const min=config?.min_password_length||10
  const remoteBlocked=!!config?.setup_local_only&&!config?.setup_allowed_here
  const mismatch=confirmPassword!==''&&password!==confirmPassword

  const submit=async(e:FormEvent)=>{
    e.preventDefault()
    if(busy)return
    if(password!==confirmPassword){setError('两次输入的密码不一致');return}
    setBusy(true);setError('')
    try{
      await setupAdmin({username:username.trim(),display_name:displayName.trim(),password,confirm_password:confirmPassword})
      onDone()
    }catch(err:any){
      setError(err?.message||'初始化失败')
    }finally{
      setBusy(false)
    }
  }

  return <div className="authPage">
    <form className="authCard" onSubmit={submit}>
      <div className="authHead">
        <div className="brandMark lg">AI</div>
        <div><h1>创建管理员账号</h1><p>首次启动 · 校园模型网关</p></div>
      </div>

      {remoteBlocked
        ? <div className="authError"><AlertTriangle/>出于安全考虑，首次初始化只允许在服务器本机进行。请在服务器上打开 <code>http://127.0.0.1:8080/admin-ui</code> 完成创建。</div>
        : <div className="authNotice"><ShieldCheck/>这是首次启动，请创建唯一的超级管理员账号。该账号拥有全部管理权限。</div>}

      <label className="field"><span>用户名</span>
        <input autoFocus autoComplete="username" value={username} placeholder="用于日常登录，例如 admin"
          onChange={e=>setUsername(e.target.value)}/>
      </label>
      <label className="field"><span>显示名称（可选）</span>
        <input value={displayName} placeholder="例如 信息化中心管理员"
          onChange={e=>setDisplayName(e.target.value)}/>
      </label>
      <label className="field"><span>密码</span>
        <PasswordInput autoComplete="new-password" value={password} placeholder={`至少 ${min} 位`}
          onChange={setPassword}/>
        <small className="hint">至少 {min} 位，且需包含字母、数字、符号中的至少两类；不能与用户名相同。</small>
      </label>
      <label className="field"><span>确认密码</span>
        <PasswordInput autoComplete="new-password" value={confirmPassword} placeholder="请再次输入密码"
          onChange={setConfirmPassword}/>
      </label>
      {mismatch&&<div className="authError"><AlertTriangle/>两次输入的密码不一致</div>}
      {error&&<div className="authError"><AlertTriangle/>{error}</div>}
      <button className="btn primary authSubmit" type="submit"
        disabled={busy||remoteBlocked||!username.trim()||!password||password!==confirmPassword}>
        {busy?<><Loader2 className="spin"/>正在创建</>:<><ShieldCheck/>创建并登录</>}
      </button>
    </form>
  </div>
}

// ---------------------------------------------------------------------------
// Account and security
// ---------------------------------------------------------------------------

function AccountPage({who,onNotice,onSignedOut}:{who:WhoAmI|null;onNotice:(t:string,bad?:boolean)=>void;onSignedOut:()=>void}){
  const [current,setCurrent]=useState('')
  const [next,setNext]=useState('')
  const [confirmNext,setConfirmNext]=useState('')
  const [error,setError]=useState('')
  const [busy,setBusy]=useState(false)
  const [sessions,setSessions]=useState<AdminSessionView[]>([])
  const [loadingSessions,setLoadingSessions]=useState(true)

  const mode=(who?.auth_mode||'').toLowerCase()
  const sessionMode=mode==='session'

  const reloadSessions=async()=>{
    setLoadingSessions(true)
    try{setSessions(await listSessions())}
    catch{/* the global 401 handler deals with an expired session */}
    finally{setLoadingSessions(false)}
  }
  // Only session mode owns a device list; asking for it under trusted-header
  // would just produce a 401 for no reason.
  useEffect(()=>{if(sessionMode)reloadSessions()},[sessionMode])

  const submit=async(e:FormEvent)=>{
    e.preventDefault()
    if(busy)return
    if(next!==confirmNext){setError('两次输入的新密码不一致');return}
    setBusy(true);setError('')
    try{
      const out=await changePassword(current,next)
      onNotice(out?.message||'密码已更新')
      setCurrent('');setNext('');setConfirmNext('')
      // Every session was revoked by the change, so return to the login screen.
      clearSession()
      onSignedOut()
    }catch(err:any){
      setError(err?.message||'修改密码失败')
    }finally{
      setBusy(false)
    }
  }

  const revoke=async(id:string)=>{
    if(!confirm('确认撤销该设备的登录状态？'))return
    try{
      await revokeSession(id)
      if(sessions.find(s=>s.id===id)?.current){onNotice('当前会话已撤销，请重新登录');onSignedOut();return}
      onNotice('已撤销该设备的登录状态')
      await reloadSessions()
    }catch(err:any){onNotice(err?.message||'撤销失败',true)}
  }

  // Trusted-header mode: the campus reverse proxy owns the credential, so there
  // is no password to rotate and no local device list to manage. Showing those
  // forms would imply this console holds a credential it does not have.
  if(!sessionMode){
    return <div className="settingsGrid">
      <Panel title="当前身份" sub="身份由校园统一身份认证注入，权限由认证层提供的角色决定。">
        <div className="kv">
          <span>用户名</span><b>{who?.username||who?.user||'—'}</b>
          <span>角色</span><b>{who?.roles?.join('、')||'—'}</b>
          <span>认证方式</span><b>学校统一身份认证</b>
        </div>
      </Panel>
      <Panel title="账号与安全" sub="密码与登录设备由校园统一身份认证平台管理。">
        <p className="authHint">
          本控制台不保存账号密码。如需修改密码或注销其他设备，请前往校园统一身份认证平台操作。
        </p>
      </Panel>
    </div>
  }

  return <div className="settingsGrid">
    <Panel title="当前身份" sub="身份来自账号密码登录，权限由账号角色决定。">
      <div className="kv">
        <span>用户名</span><b>{who?.username||who?.user||'—'}</b>
        <span>角色</span><b>{who?.roles?.join('、')||'—'}</b>
        <span>认证方式</span><b>账号密码登录</b>
        <span>登录状态</span><b>安全会话 · 自动续期</b>
      </div>
      <div className="panelFoot"><button className="btn secondary" onClick={onSignedOut}><LogOut/>退出登录</button></div>
    </Panel>

    <Panel title="修改密码" sub="修改成功后，所有设备上的登录状态都会立即失效，需要用新密码重新登录。">
      <form className="form" onSubmit={submit}>
        <F label="当前密码"><PasswordInput autoComplete="current-password" value={current} onChange={setCurrent}/></F>
        <F label="新密码" hint="至少 10 位，且需包含字母、数字、符号中的至少两类。">
          <PasswordInput autoComplete="new-password" value={next} onChange={setNext}/>
        </F>
        <F label="确认新密码"><PasswordInput autoComplete="new-password" value={confirmNext} onChange={setConfirmNext}/></F>
        {error&&<div className="authError"><AlertTriangle/>{error}</div>}
        <div className="formActions">
          <button className="btn primary" type="submit" disabled={busy||!current||!next||!confirmNext}>
            {busy?<><Loader2 className="spin"/>正在更新</>:<><KeyRound/>更新密码</>}
          </button>
        </div>
      </form>
    </Panel>

    <Panel title="登录设备" sub="这里列出当前账号仍然有效的登录会话。撤销后该设备需要重新登录。">
      {loadingSessions
        ? <div className="empty"><Loader2 className="spin"/><b>正在读取登录设备</b></div>
        : sessions.length
          ? <Table heads={['设备 / 来源','登录方式','最近活动','到期时间','']}>
              {sessions.map(s=><tr key={s.id}>
                <td><b>{s.ip||'未知地址'}</b>{s.current&&<span className="badge ok">当前设备</span>}<br/><span className="muted cut">{s.user_agent||'未知客户端'}</span></td>
                <td><span className="badge">{s.login_method==='password'?'密码登录':s.login_method}</span></td>
                <td>{when(s.last_active_at)}</td>
                <td>{when(s.expires_at)}</td>
                <td><button className="link dangerText" onClick={()=>revoke(s.id)}>撤销</button></td>
              </tr>)}
            </Table>
          : <Empty text="没有其他有效登录会话"/>}
    </Panel>
  </div>
}

// ---------------------------------------------------------------------------
// Shared presentational helpers
// ---------------------------------------------------------------------------

// AdminsPage lets a super administrator create accounts and assign roles. The
// server refuses the same operations for every other role, so this page is a
// convenience layer over the API rather than the security boundary.
function AdminsPage({items,tenants,currentUsername,onRefresh,onNotice}:{items:AdminAccount[];tenants:Tenant[];currentUsername:string;onRefresh:()=>Promise<void>;onNotice:(t:string,bad?:boolean)=>void}){
  const [creating,setCreating]=useState(false)
  const [editing,setEditing]=useState('')
  const [editRoles,setEditRoles]=useState<string[]>([])
  const [editScope,setEditScope]=useState('')
  const [draft,setDraft]=useState({username:'',display_name:'',password:'',roles:['auditor'] as string[],scope_tenant_id:''})
  const [busy,setBusy]=useState(false)
  const [error,setError]=useState('')

  const toggle=(list:string[],role:string)=>list.includes(role)?list.filter(r=>r!==role):[...list,role]

  // The scope select lists every tenant in tree order, indented by depth. An
  // empty value means "no scope", which reaches every tenant -- that is a real
  // grant, so it is labelled as such rather than left blank.
  const scopeOptions=<>
    <option value="">（无作用域 · 可管理全部租户）</option>
    {tenants.map(t=><option key={t.id} value={t.id}>{'　'.repeat(t.depth||0)}{t.name}（{t.code}）</option>)}
  </>

  async function submitCreate(e:FormEvent){
    e.preventDefault();setError('')
    if(!draft.username.trim()){setError('请填写用户名。');return}
    if(!draft.roles.length){setError('请至少选择一个角色。');return}
    setBusy(true)
    try{
      await createAdminUser({
        username:draft.username.trim(),display_name:draft.display_name.trim(),
        password:draft.password,roles:draft.roles,
        scope_tenant_id:draft.scope_tenant_id||null,
      })
      onNotice('管理员账号已创建')
      setDraft({username:'',display_name:'',password:'',roles:['auditor'],scope_tenant_id:''})
      setCreating(false)
      await onRefresh()
    }catch(err:any){setError(err?.message||'创建失败')}
    finally{setBusy(false)}
  }

  async function saveRoles(id:string){
    if(!editRoles.length){onNotice('请至少保留一个角色',true);return}
    setBusy(true)
    try{
      await updateAdminUser(id,{roles:editRoles})
      await setAdminUserScope(id,editScope||null)
      onNotice('角色与作用域已更新')
      setEditing('')
      await onRefresh()
    }catch(err:any){onNotice(err?.message||'更新失败',true)}
    finally{setBusy(false)}
  }

  async function toggleStatus(u:AdminAccount){
    const next=u.status===1?2:1
    if(next===2&&!confirm('确认停用「'+u.username+'」？该账号会立即被踢下线。'))return
    setBusy(true)
    try{
      await updateAdminUser(u.id,{status:next})
      onNotice(next===2?'账号已停用':'账号已启用')
      await onRefresh()
    }catch(err:any){onNotice(err?.message||'操作失败',true)}
    finally{setBusy(false)}
  }

  return <div className="settingsGrid">
    <Panel title="管理员账号" sub="只有超级管理员可以创建账号与分配角色。角色变更即时生效，无需重新登录。">
      <div className="panelFoot">
        <button className="btn primary" disabled={busy} onClick={()=>{setCreating(v=>!v);setError('')}}>
          {creating?<><X/>取消</>:<><UserCog/>新建管理员</>}
        </button>
      </div>
      {creating&&<form className="form" onSubmit={submitCreate}>
        <div className="formGrid">
          <F label="用户名"><input value={draft.username} autoComplete="off" onChange={e=>setDraft({...draft,username:e.target.value})}/></F>
          <F label="显示名"><input value={draft.display_name} autoComplete="off" onChange={e=>setDraft({...draft,display_name:e.target.value})}/></F>
        </div>
        <F label="初始密码" hint="至少 10 位，且需包含字母、数字、符号中的至少两类。请通过安全渠道交给本人。">
          <input type="password" autoComplete="new-password" value={draft.password} onChange={e=>setDraft({...draft,password:e.target.value})}/>
        </F>
        <F label="角色">
          <div className="rolePicker">{ADMIN_ROLES.map(r=><label key={r} className={'roleChip '+(draft.roles.includes(r)?'on':'')}><input type="checkbox" checked={draft.roles.includes(r)} onChange={()=>setDraft({...draft,roles:toggle(draft.roles,r)})}/><span>{roleLabel(r)}</span></label>)}</div>
        </F>
        <F label="租户作用域" hint="指定后，该账号只能管理这个租户的子孙节点（不含该租户自身，避免自行加额度）。">
          <select value={draft.scope_tenant_id} onChange={e=>setDraft({...draft,scope_tenant_id:e.target.value})}>{scopeOptions}</select>
        </F>
        {error&&<div className="authError"><AlertTriangle/>{error}</div>}
        <div className="formActions">
          <button className="btn primary" type="submit" disabled={busy||!draft.username.trim()||!draft.password}>
            {busy?<><Loader2 className="spin"/>正在创建</>:<><UserCog/>创建账号</>}
          </button>
        </div>
      </form>}
      {items.length
        ? <Table heads={['用户名','显示名','角色','作用域','状态','最后登录','操作']}>
            {items.map(u=><tr key={u.id}>
              <td><b>{u.username}</b>{u.username===currentUsername&&<span className="badge ok">当前账号</span>}</td>
              <td>{u.display_name||'—'}</td>
              <td>{editing===u.id
                ? <div className="rolePicker">{ADMIN_ROLES.map(r=><label key={r} className={'roleChip '+(editRoles.includes(r)?'on':'')}><input type="checkbox" checked={editRoles.includes(r)} onChange={()=>setEditRoles(toggle(editRoles,r))}/><span>{roleLabel(r)}</span></label>)}</div>
                : <span className="roleTags">{u.roles.map(r=><span key={r} className="badge">{roleLabel(r)}</span>)}</span>}</td>
              <td>{editing===u.id
                ? <select value={editScope} onChange={e=>setEditScope(e.target.value)}>{scopeOptions}</select>
                : (u.scope_tenant_id
                    ? <span className="badge">{tenants.find(t=>t.id===u.scope_tenant_id)?.name||u.scope_tenant_id}</span>
                    : <span className="badge warn">全部租户</span>)}</td>
              <td>{status(u.status)}</td>
              <td>{when(u.last_login_at)}</td>
              <td>
                {editing===u.id
                  ? <div className="rowActions"><button className="btn primary" disabled={busy||!editRoles.length} onClick={()=>saveRoles(u.id)}>保存</button><button className="btn secondary" onClick={()=>setEditing('')}>取消</button></div>
                  : <div className="rowActions">
                      <button className="btn secondary" disabled={u.username===currentUsername} title={u.username===currentUsername?'不能修改自己的角色与作用域':''} onClick={()=>{setEditing(u.id);setEditRoles(u.roles.slice());setEditScope(u.scope_tenant_id||'')}}>改角色/作用域</button>
                      <button className="btn secondary" disabled={u.username===currentUsername} title={u.username===currentUsername?'不能停用自己的账号':''} onClick={()=>toggleStatus(u)}>{u.status===1?'停用':'启用'}</button>
                    </div>}
              </td>
            </tr>)}
          </Table>
        : <Empty text="还没有管理员账号"/>}
    </Panel>
  </div>
}

function Table({heads,children}:{heads:string[];children:any}){return <div className="tableWrap"><table><thead><tr>{heads.map(x=><th key={x}>{x}</th>)}</tr></thead><tbody>{children}</tbody></table></div>}

function DrawerView({drawer,providers,tenants,models,onClose,onProvider,onModel,onRoute,onTenant,onAllocate,onModelQuota,onKey,onTest}:{drawer:Drawer;providers:Provider[];tenants:Tenant[];models:ModelConfig[];onClose:()=>void;onProvider:(v:any)=>void;onModel:(v:any)=>void;onRoute:(m:ModelConfig,v:any)=>void;onTenant:(v:any)=>void;onAllocate:(id:string,v:TenantAllocation)=>void;onModelQuota:(v:ModelQuotaAllocation)=>void;onKey:(v:any)=>void;onTest:(m:string,p:string)=>void}){
 if(!drawer)return null
 const d=drawer.data||{}
 return <div className="overlay" onMouseDown={e=>{if(e.target===e.currentTarget)onClose()}}><aside className="drawer"><div className="drawerHead"><div><span className="eyebrow">ADMIN</span><h2>{drawerTitle(drawer.kind)}</h2></div><button className="iconBtn" onClick={onClose}><X/></button></div><div className="drawerBody">
  {drawer.kind==='provider'&&<ProviderForm value={d} onSave={onProvider}/>}
  {drawer.kind==='model'&&<ModelForm value={d} providers={providers} onSave={onModel}/>}
  {drawer.kind==='route'&&<RouteForm value={d.route} model={d.model} providers={providers} onSave={onRoute}/>}
  {drawer.kind==='tenant'&&<TenantForm value={d} tenants={tenants} onSave={onTenant}/>}
  {drawer.kind==='allocate'&&<AllocationPanel tenant={d.tenant} view={d.view} models={models} onSave={onAllocate}/>}
  {drawer.kind==='modelQuota'&&<ModelQuotaForm value={d} tenants={tenants} models={models} onSave={onModelQuota}/>}
  {drawer.kind==='key'&&<KeyForm value={d} tenants={tenants} models={models} onSave={onKey}/>}
  {drawer.kind==='log'&&<LogDetail v={d}/>}
  {drawer.kind==='security'&&<SecurityDetail v={d}/>}
  {drawer.kind==='usage'&&<UsageDetail data={d}/>}
  {drawer.kind==='secret'&&<Secret value={d}/>}
  {drawer.kind==='test'&&<TestForm value={d} models={models} onTest={onTest}/>}
  {drawer.kind==='testResult'&&<TestResult value={d}/>}
 </div></aside></div>
}
const drawerTitle=(k:string)=>({provider:'供应商配置',model:'对外模型配置',route:'线路配置',tenant:'租户配置',allocate:'额度分配',modelQuota:'模型额度配置',key:'API Key 配置',log:'请求详情',security:'安全事件详情',usage:'租户用量',secret:'新 Key 已生成',test:'模型测试',testResult:'模型测试结果'} as any)[k]||'详情'

function ProviderForm({value,onSave}:{value:any;onSave:(v:any)=>void}){
 const [v,setV]=useState(value)
 const [tpl,setTpl]=useState('custom')
 const isNew=!value.id
 const applyTemplate=(id:string)=>{
   setTpl(id)
   const t=providerTemplates.find(x=>x.id===id)
   if(!t)return
   // 模板只预填默认值；自定义模板的默认值为空，即从空白表单开始手填。
   setV((prev:any)=>({...prev,...t.values}))
 }
 return <Form onSubmit={()=>onSave(v)}>
  {isNew&&<F label="供应商模板" hint="模板只用于预填默认值，选择后所有字段仍可手工修改。">
   <select value={tpl} onChange={e=>applyTemplate(e.target.value)}>{providerTemplates.map(t=><option key={t.id} value={t.id}>{t.label}</option>)}</select>
  </F>}
  <F label="供应商名称"><input value={v.name||''} onChange={e=>setV({...v,name:e.target.value})}/></F>
  <F label="协议"><select value={v.protocol||'openai-chat'} onChange={e=>setV({...v,protocol:e.target.value})}><option>openai-chat</option><option>openai-responses</option><option>anthropic</option><option>gemini</option></select></F>
  <F label="Base URL"><input value={v.base_url||''} onChange={e=>setV({...v,base_url:e.target.value})}/></F>
  <F label="凭据环境变量"><input className="mono" value={v.credential_env||''} onChange={e=>setV({...v,credential_env:e.target.value})}/></F>
  <div className="notice"><AlertTriangle/>这里只保存凭据环境变量名称，不保存真实厂商 API Key。</div>
  <StatusField v={v} setV={setV}/>
 </Form>
}
function ModelForm({value,providers,onSave}:{value:any;providers:Provider[];onSave:(v:any)=>void}){const [v,setV]=useState(()=>({...value,provider_id:value.provider_id||providers[0]?.id||''}));return <Form onSubmit={()=>onSave(v)}><F label="对外模型名称" hint="业务系统调用 /v1/* API 时使用的 model 名称。"><input value={v.alias||''} onChange={e=>setV({...v,alias:e.target.value})}/></F><F label="主供应商" hint="默认使用的模型供应商。"><select value={v.provider_id||providers[0]?.id||''} onChange={e=>setV({...v,provider_id:e.target.value})}>{providers.map(p=><option key={p.id} value={p.id}>{p.name}</option>)}</select></F><F label="主线路模型" hint="该供应商实际调用的上游模型名称。"><input value={v.upstream_model||''} onChange={e=>setV({...v,upstream_model:e.target.value})}/></F><div className="notice"><Route/>保存后可继续添加备用线路，用于故障切换。</div><StatusField v={v} setV={setV}/></Form>}
function RouteForm({value,model,providers,onSave}:{value:any;model:ModelConfig;providers:Provider[];onSave:(m:ModelConfig,v:any)=>void}){const [v,setV]=useState(()=>({...value,provider_id:value.provider_id||providers[0]?.id||''}));return <Form onSubmit={()=>onSave(model,v)}><div className="notice"><Route/>业务仍然调用 <b>{model.alias}</b>，这里只配置实际上游线路。</div><F label="供应商"><select value={v.provider_id||providers[0]?.id||''} onChange={e=>setV({...v,provider_id:e.target.value})}>{providers.map(p=><option key={p.id} value={p.id}>{p.name}</option>)}</select></F><F label="上游模型"><input value={v.upstream_model||''} onChange={e=>setV({...v,upstream_model:e.target.value})}/></F><div className="formGrid"><F label="优先级"><NumInput value={v.priority} placeholder="1" onValue={n=>setV((prev:any)=>({...prev,priority:n??0}))}/></F><F label="权重"><NumInput value={v.weight} placeholder="0" onValue={n=>setV((prev:any)=>({...prev,weight:n??0}))}/></F><F label="超时时间（ms）"><NumInput value={v.timeout_ms} placeholder="60000" onValue={n=>setV((prev:any)=>({...prev,timeout_ms:n??0}))}/></F></div><StatusField v={v} setV={setV}/></Form>}
function TenantForm({value,tenants,onSave}:{value:any;tenants:Tenant[];onSave:(v:any)=>void}){
 const [v,setV]=useState(value)
 const [errors,setErrors]=useState<Record<string,string>>({})
 // A tenant cannot be its own parent, and moving a node under its own subtree
 // would detach it from the tree, so descendants are filtered out too.
 const forbidden=new Set<string>([v.id,...tenants.filter(t=>v.id&&t.path.includes('/'+v.id+'/')).map(t=>t.id)].filter(Boolean) as string[])
 const parents=tenants.filter(t=>!forbidden.has(t.id))
 // Validate before the round trip. The server answers with one generic message
 // ("code and name are required"), so an operator who filled in the name but
 // missed the code got a toast that never said which field was at fault.
 const submit=()=>{
  const next:Record<string,string>={}
  if(!String(v.code||'').trim()) next.code='请填写租户编码'
  if(!String(v.name||'').trim()) next.name='请填写租户名称'
  setErrors(next)
  if(Object.keys(next).length) return
  onSave(v)
 }
 const setField=(k:string,val:any)=>{
  setV((prev:any)=>({...prev,[k]:val}))
  setErrors(prev=>{if(!prev[k])return prev;const n={...prev};delete n[k];return n})
 }
 return <Form onSubmit={submit}>
  <div className="formGrid">
   <F label="租户编码" error={errors.code}>
    <input className={errors.code?'invalid':undefined} value={v.code||''} onChange={e=>setField('code',e.target.value)}/></F>
   <F label="租户名称" error={errors.name}>
    <input className={errors.name?'invalid':undefined} value={v.name||''} onChange={e=>setField('name',e.target.value)}/></F>
  </div>
  <div className="formGrid">
   <F label="层级类型"><select value={v.kind||'tenant'} onChange={e=>setV({...v,kind:e.target.value})}>{TENANT_KINDS.map(k=><option key={k} value={k}>{tenantKindLabel(k)}（{k}）</option>)}</select></F>
   <F label="上级租户" hint="留空即根租户；创建后改用「分配额度」调额度">
    <select value={v.parent_id||''} onChange={e=>setV({...v,parent_id:e.target.value||null})}>
     <option value="">（无 · 根租户）</option>
     {parents.map(t=><option key={t.id} value={t.id}>{'　'.repeat(t.depth||0)}{t.name}（{t.code}）</option>)}
    </select>
   </F>
  </div>
  {v.id&&<div className="formNote">父级变更请用租户卡片上的「编辑」保存；额度请用「分配额度」，那里会校验不能超过上级持有量。</div>}
  <QuotaFields v={v} setV={setV}/>
  <StatusField v={v} setV={setV}/>
 </Form>
}

// NumInput / F / QuotaFields live in ./fields so they can be unit tested.


// AllocationPanel is the delegation view: it shows what the parent level holds
// (the ceiling), what this node currently inherits, and lets the operator hand
// down a narrower budget. The ceiling is not decoration -- the server rejects
// anything above it, so showing it up front is what keeps the form usable.
function AllocationPanel({tenant,view,models,onSave}:{tenant:Tenant;view:any;models:ModelConfig[];onSave:(id:string,v:TenantAllocation)=>void}){
 const eff:EffectiveLimits|undefined=view?.effective
 const effModels:EffectiveModelQuota[]=view?.effective_models||[]
 const [form,setForm]=useState<Record<string,number|null>>({
  daily_token_limit:tenant.daily_token_limit,
  monthly_token_limit:tenant.monthly_token_limit,
  monthly_request_limit:tenant.monthly_request_limit,
  monthly_budget_micros:tenant.monthly_budget_micros,
 })
 // null = inherit, so "inherit" is the default and must survive a round trip.
 const [modelsMode,setModelsMode]=useState<'inherit'|'list'|'deny'>(tenant.allowed_models===null?'inherit':(tenant.allowed_models.length?'list':'deny'))
 const [picked,setPicked]=useState<string[]>(tenant.allowed_models||[])
 const set=(k:string,val:number|null)=>setForm(f=>({...f,[k]:val}))

 const submit=()=>{
  const body:TenantAllocation={...form}
  body.allowed_models=modelsMode==='inherit'?null:(modelsMode==='deny'?[]:picked)
  onSave(tenant.id,body)
 }

 const dims:[string,string,number|undefined][]=[
  ['daily_token_limit','每日 Token',eff?.daily_token_limit],
  ['monthly_token_limit','每月 Token',eff?.monthly_token_limit],
  ['monthly_request_limit','每月请求',eff?.monthly_request_limit],
  ['monthly_budget_micros','月预算 micros',eff?.monthly_budget_micros],
 ]
 return <div className="allocPanel">
  <div className="allocTarget"><span className="eyebrow">{tenant.code} · {tenantKindLabel(tenant.kind)}</span><h3>{tenant.name}</h3></div>
  <Detail title="当前生效值（已含继承）" rows={dims.map(([k,label,val])=>[label,val?fmt(val):'不限额'])}/>
  {/* The model axis resolves the same way (inherit from the nearest level that
      sets a value) but is configured on its own page, because a model quota is
      a map of model -> numbers rather than one row of numbers. Showing the
      resolved result here keeps "why does it stop at 100?" answerable in one
      place. */}
  {effModels.length>0&&<Detail title="按模型生效额度（已含继承）" rows={effModels.map((m:EffectiveModelQuota)=>[m.model_alias,
    (m.own?'本租户设定':'继承自 '+m.set_by)+' · 日 '+(m.daily_token_limit?fmt(m.daily_token_limit):'不限')+' · 月 '+(m.monthly_token_limit?fmt(m.monthly_token_limit):'不限')])}/>}
  <Form onSubmit={submit}>
   <div className="formGrid">
    {dims.map(([k,label])=>(
     <F key={k} label={label} hint="留空=继承 · 0=不限额">
      <NumInput value={form[k]} onValue={n=>set(k,n)}/>
     </F>
    ))}
   </div>
   <F label="可用模型" hint="只能收窄，不能超出上级白名单">
    <select value={modelsMode} onChange={e=>setModelsMode(e.target.value as any)}>
     <option value="inherit">继承上级</option>
     <option value="list">指定白名单</option>
     <option value="deny">禁止全部模型</option>
    </select>
   </F>
   {modelsMode==='list'&&<div className="checks">{models.filter(m=>m.status===1).map(m=><label key={m.id}><input type="checkbox" checked={picked.includes(m.alias)} onChange={e=>setPicked(e.target.checked?[...picked,m.alias]:picked.filter(x=>x!==m.alias))}/>{m.alias}</label>)}</div>}
   {eff&&eff.model_access_restricted&&<div className="formNote">上级当前可用的模型：{eff.allowed_models?.length?eff.allowed_models.join('、'):'（无，上级已禁止全部）'}</div>}
  </Form>
 </div>
}
function KeyForm({value,tenants,models,onSave}:{value:any;tenants:Tenant[];models:ModelConfig[];onSave:(v:any)=>void}){const [v,setV]=useState(()=>({...value,tenant_id:value.tenant_id||tenants[0]?.id||''}));const allowed=v.allowed_models||[];return <Form onSubmit={()=>onSave(v)}><F label="名称"><input value={v.name||''} onChange={e=>setV({...v,name:e.target.value})}/></F><F label="租户"><select value={v.tenant_id||tenants[0]?.id||''} onChange={e=>setV({...v,tenant_id:e.target.value})}>{tenants.map(t=><option key={t.id} value={t.id}>{t.name} ({t.code})</option>)}</select></F><F label="允许模型"><div className="checks">{models.map(m=><label key={m.id}><input type="checkbox" checked={allowed.includes(m.alias)} onChange={e=>setV({...v,allowed_models:e.target.checked?[...allowed,m.alias]:allowed.filter((x:string)=>x!==m.alias)})}/>{m.alias}</label>)}</div></F><F label="允许 IP / CIDR（每行一个）"><textarea rows={4} value={(v.allowed_ips||[]).join('\n')} onChange={e=>setV({...v,allowed_ips:e.target.value.split(/\n|,/).map((x:string)=>x.trim()).filter(Boolean)})}/></F><div className="formGrid"><Num label="RPM" k="rpm_limit" v={v} setV={setV}/><Num label="TPM" k="tpm_limit" v={v} setV={setV}/><Num label="最大并发" k="max_concurrency" v={v} setV={setV}/><F label="Route Group"><input value={v.route_group||''} onChange={e=>setV({...v,route_group:e.target.value})}/></F></div><F label="过期时间（ISO，可留空）"><input value={v.expire_at||''} onChange={e=>setV({...v,expire_at:e.target.value||undefined})}/></F><StatusField v={v} setV={setV}/></Form>}
function Num({label,k,v,setV}:{label:string;k:string;v:any;setV:any}){return <F label={label}><NumInput value={v[k]??0} placeholder="0" onValue={n=>setV((prev:any)=>({...prev,[k]:n??0}))}/></F>}
function StatusField({v,setV}:{v:any;setV:any}){return <F label="状态"><select value={v.status||1} onChange={e=>setV({...v,status:+e.target.value})}><option value="1">启用</option><option value="2">停用</option></select></F>}
function Form({children,onSubmit}:{children:any;onSubmit:()=>void}){return <form className="form" onSubmit={e=>{e.preventDefault();onSubmit()}}>{children}<div className="formActions"><button className="btn primary" type="submit">保存</button></div></form>}

function LogDetail({v}:{v:RequestLog}){return <div className="detailStack"><Detail title="基础信息" rows={[['Request ID',v.request_id],['时间',when(v.created_at)],['Tenant',v.tenant_id],['API Key',v.api_key_id],['来源 IP',v.client_ip],['HTTP Status',v.status_code],['Error',v.error_code||'—']]}/><Detail title="线路" rows={[['请求模型',v.requested_model],['实际模型',v.actual_model||'未知'],['供应商',v.provider||'未知'],['上游模型',v.upstream_model||'未知'],['线路优先级',v.route_priority||'未知'],['尝试次数',v.attempts||'未知'],['故障切换',v.failover?'是':'否']]}/><Detail title="Usage" rows={[['Input Tokens',v.input_tokens],['Output Tokens',v.output_tokens],['Total Tokens',v.total_tokens]]}/><Detail title="Timing" rows={[['Latency',ms(v.latency_ms)],['Stream',v.stream?'是':'否']]}/></div>}
function SecurityDetail({v}:{v:SecurityEvent}){return <Detail title={v.event_type} rows={[['时间',when(v.created_at)],['Request ID',v.request_id||'—'],['Tenant',v.tenant_id||'—'],['API Key',v.api_key_id||'—'],['来源 IP',v.client_ip||'—'],['模型',v.requested_model||'—'],['详情',v.detail||'—']]}/>}
function Detail({title,rows}:{title:string;rows:[string,any][]}){return <section className="detailBox"><h3>{title}</h3>{rows.map(([k,v])=><div className="detailRow" key={k}><span>{k}</span><b className={String(v).length>35?'mono wrap':''}>{unknown(v)}</b></div>)}</section>}
function UsageDetail({data}:{data:{tenant:Tenant;usage:TenantUsage;effective?:EffectiveLimits}}){
 const {tenant:t,usage:u,effective:e}=data
 return <div className="detailStack">
  <Detail title={t.name} rows={[['今日请求',u.daily_requests],['本月请求',u.monthly_requests],['今日 Tokens',u.daily_tokens],['本月 Tokens',u.monthly_tokens]]}/>
  <section className="detailBox"><h3>配额（生效值）</h3>
   <Progress label="日 Token" used={u.daily_tokens} limit={e?e.daily_token_limit:t.daily_token_limit}/>
   <Progress label="月 Token" used={u.monthly_tokens} limit={e?e.monthly_token_limit:t.monthly_token_limit}/>
   <Progress label="月请求" used={u.monthly_requests} limit={e?e.monthly_request_limit:t.monthly_request_limit}/>
   {e&&<div className="formNote">{e.model_access_restricted
     ?'可用模型：'+(e.allowed_models?.length?e.allowed_models.join('、'):'（已禁止全部模型）')
     :'可用模型：不限（未配置白名单）'}</div>}
  </section>
 </div>
}
function Progress({label,used,limit}:{label:string;used:number;limit:number|null|undefined}){
 // A null limit means "inherited from an ancestor that sets nothing", which is
 // not the same as 0 ("unlimited"). Both end up with no bar, but the label has
 // to say which one it is.
 const unlimited=limit===null||limit===undefined||limit===0
 const pct=unlimited?0:Math.min(100,used/limit*100)
 const text=limit===null||limit===undefined?'继承上级':(limit===0?'不限额':fmt(limit))
 return <div className="progress"><div><span>{label}</span><b>{fmt(used)} / {text}</b></div><i><em style={{width:pct+'%'}}/></i></div>
}
function Secret({value}:{value:string}){return <div className="secretBox"><AlertTriangle/><h3>请立即保存完整 Key</h3><p>完整 Key 只显示一次，刷新或关闭后无法再次查看。</p><code>{value}</code><button className="btn primary" onClick={()=>navigator.clipboard.writeText(value)}>复制 Key</button></div>}
function TestForm({value,models,onTest}:{value:any;models:ModelConfig[];onTest:(m:string,p:string)=>void}){const [m,setM]=useState(value.model);const [p,setP]=useState('请只回复：MODEL_OK');return <Form onSubmit={()=>onTest(m,p)}><F label="对外模型"><select value={m} onChange={e=>setM(e.target.value)}>{models.filter(x=>x.status===1).map(x=><option key={x.id}>{x.alias}</option>)}</select></F><F label="Prompt"><textarea rows={5} value={p} onChange={e=>setP(e.target.value)}/></F></Form>}
function TestResult({value:v}:{value:any}){return <div className="detailStack"><div className="secretBox success"><Zap/><h3>模型测试成功</h3><p>{v.content||'(空响应)'}</p></div><Detail title="调用指标" rows={[['Model',v.model],['Input Tokens',v.input_tokens],['Output Tokens',v.output_tokens],['Total Tokens',v.total_tokens],['Latency',ms(v.latency_ms||0)]]}/></div>}

export default App
