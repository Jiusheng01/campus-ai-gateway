// Shared form primitives.
//
// These live outside App.tsx so they can be unit tested directly. They are the
// pieces that carry the console's numeric semantics, which is where the subtle
// bugs live (see NumInput below).
import {useEffect,useRef,useState} from 'react'

export function fmtNum(v:number|null|undefined){return v===null||v===undefined?'':String(v)}

// quotaLabel renders a tri-state quota value for display.
//
// The three states are genuinely different and must read differently:
//
//	null -> "继承上级": the value comes from the nearest ancestor that sets one
//	0    -> "不限额":   explicitly uncapped, which stops the inheritance walk
//	>0   -> the number itself
//
// Collapsing null and 0 into "不限" -- the obvious shortcut -- hides the one
// distinction the hierarchy depends on, and an operator who cannot tell whether
// a field is inherited or deliberately uncapped cannot debug a quota.
//
// It is a plain function rather than a component so both the card view and the
// table view render the same words, and so the rule can be tested on its own.
// The formatter is injected because the console abbreviates large numbers and
// this module has no opinion about that.
export function quotaLabel(n:number|null|undefined,format:(n:number)=>string=String){
 if(n===null||n===undefined)return '继承上级'
 return n?format(n):'不限额'
}

// NumInput is the numeric field used throughout the console.
//
// It is deliberately NOT <input type="number">. A number input cannot host an
// IME composition, so with a Chinese input method active the keystrokes are
// swallowed and the only way to change a value is the spinner -- which is
// exactly the bug operators hit on the quota fields. A text input with
// inputMode="numeric" keeps the numeric keypad on touch devices, lets IME input
// through, and gives us full control over what counts as a valid value.
//
// The draft is held as a string so intermediate states ("0", "00", "007")
// survive: echoing String(Number(x)) straight back would eat the second "0" and
// make typing look broken.
//
// `null` means "inherit from the parent level" and is distinct from 0
// ("unlimited"), so an empty field must round-trip as null and never as 0.
export function NumInput({value,onValue,placeholder='继承',invalid,disabled}:{
 value:number|null|undefined
 onValue:(v:number|null)=>void
 placeholder?:string
 invalid?:boolean
 disabled?:boolean
}){
 const [draft,setDraft]=useState(()=>fmtNum(value))
 const last=useRef<number|null>(value===undefined?null:value)

 // Adopt values pushed in from outside (another record loaded into the same
 // form, a server echo) without fighting the operator mid-edit: only resync
 // when the incoming value differs from what we last reported.
 useEffect(()=>{
  const next=value===undefined?null:value
  if(next!==last.current){last.current=next;setDraft(fmtNum(next))}
 },[value])

 const handle=(raw:string)=>{
  // Digits only. A stray "-", "e" or CJK character would otherwise reach
  // Number() as NaN, blank the field, and read as dropped input.
  const digits=raw.replace(/[^0-9]/g,'')
  const next=digits===''?null:Number(digits)
  last.current=next
  setDraft(digits)
  onValue(next)
 }

 return <input type="text" inputMode="numeric" autoComplete="off"
  className={invalid?'invalid':undefined}
  value={draft} placeholder={placeholder} disabled={disabled}
  onChange={e=>handle(e.target.value)}
  onBlur={()=>setDraft(fmtNum(value))}/>
}

export function F({label,children,hint,error}:{label:string;children:any;hint?:string;error?:string}){
 return <label className={error?'field invalid':'field'}>
  <span>{label}</span>{children}
  {error?<small className="err">{error}</small>:hint&&<small className="hint">{hint}</small>}
 </label>
}

// QuotaFields edits the tri-state quota values. An empty input means "inherit"
// (null on the wire), which is deliberately distinct from 0 ("unlimited").
export function QuotaFields({v,setV}:{v:any;setV:any}){
 return <div className="formGrid">
  <QuotaNum label="每日 Token 上限" k="daily_token_limit" v={v} setV={setV}/>
  <QuotaNum label="每月 Token 上限" k="monthly_token_limit" v={v} setV={setV}/>
  <QuotaNum label="每月请求上限" k="monthly_request_limit" v={v} setV={setV}/>
  <QuotaNum label="月预算 micros" k="monthly_budget_micros" v={v} setV={setV}/>
 </div>
}

export function QuotaNum({label,k,v,setV}:{label:string;k:string;v:any;setV:any}){
 return <F label={label} hint="留空=继承上级 · 0=不限额">
  <NumInput value={v[k]} onValue={n=>setV((prev:any)=>({...prev,[k]:n}))}/>
 </F>
}
