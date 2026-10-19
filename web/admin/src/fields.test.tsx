import {useEffect,useState} from 'react'
import {cleanup,render} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {afterEach,describe,expect,it} from 'vitest'

import {NumInput,QuotaFields,quotaLabel} from './fields'

// render() appends to document.body and testing-library only auto-cleans when
// the test framework exposes global hooks, which this suite deliberately does
// not. Without this, queries match nodes left behind by earlier tests.
afterEach(cleanup)

// renderNum mounts NumInput under a stateful host so the component is driven
// exactly as it is in the console: the parent echoes every change back down.
function renderNum(initial:number|null|undefined){
 const seen:(number|null)[]=[]
 function Host(){
  const [v,setV]=useState<number|null|undefined>(initial)
  return <NumInput value={v} onValue={n=>{seen.push(n);setV(n)}}/>
 }
 const utils=render(<Host/>)
 const input=utils.getByRole('textbox') as HTMLInputElement
 return {...utils,seen,input}
}

describe('NumInput',()=>{
 it('is a text input, not a number input',()=>{
  // The regression guard for the reported bug. <input type="number"> cannot
  // host an IME composition, so with a Chinese input method active every
  // keystroke was swallowed and quotas could only be changed with the spinner.
  const {input}=renderNum(null)
  expect(input.getAttribute('type')).toBe('text')
  expect(input.getAttribute('inputmode')).toBe('numeric')
 })

 it('accumulates digits as the operator types',async()=>{
  const user=userEvent.setup()
  const {input,seen}=renderNum(null)
  await user.type(input,'123')
  expect(seen).toEqual([1,12,123])
  expect(input.value).toBe('123')
 })

 it('keeps leading zeros instead of swallowing the second keystroke',async()=>{
  // Echoing String(Number(x)) straight back would turn "0" into "0" and then
  // drop the next "0" entirely, which reads as "typing does nothing".
  const user=userEvent.setup()
  const {input,seen}=renderNum(null)
  await user.type(input,'007')
  expect(input.value).toBe('007')
  expect(seen.at(-1)).toBe(7)
 })

 it('normalises the display on blur',async()=>{
  const user=userEvent.setup()
  const {input}=renderNum(null)
  await user.type(input,'007')
  await user.tab()
  expect(input.value).toBe('7')
 })

 it('treats an empty field as inherit (null), never as zero',async()=>{
  const user=userEvent.setup()
  const {input,seen}=renderNum(50)
  await user.clear(input)
  expect(seen.at(-1)).toBeNull()
  expect(input.value).toBe('')
  expect(input.placeholder).toBe('继承')
 })

 it('treats an explicit 0 as unlimited, distinct from inherit',async()=>{
  const user=userEvent.setup()
  const {input,seen}=renderNum(null)
  await user.type(input,'0')
  expect(seen.at(-1)).toBe(0)
  expect(input.value).toBe('0')
 })

 it('renders 0 as "0" and null as an empty field',()=>{
  // Scoped to each container: render() appends to document.body, so a shared
  // query would see both inputs.
  const zero=render(<NumInput value={0} onValue={()=>{}}/>)
  expect((zero.container.querySelector('input') as HTMLInputElement).value).toBe('0')
  const none=render(<NumInput value={null} onValue={()=>{}}/>)
  expect((none.container.querySelector('input') as HTMLInputElement).value).toBe('')
 })

 it('strips characters that would otherwise become NaN',async()=>{
  const user=userEvent.setup()
  const {input,seen}=renderNum(null)
  await user.type(input,'-5')
  // A "-" reaching Number() yields NaN, which blanks the field and looks like
  // dropped input. Digits-only filtering keeps the value usable.
  expect(input.value).toBe('5')
  expect(seen.at(-1)).toBe(5)
 })

 it('strips letters, separators and CJK text',async()=>{
  const user=userEvent.setup()
  const {input,seen}=renderNum(null)
  await user.type(input,'1a2')
  expect(input.value).toBe('12')
  expect(seen.at(-1)).toBe(12)

  await user.clear(input)
  await user.type(input,'1.5')
  expect(input.value).toBe('15')

  await user.clear(input)
  await user.type(input,'一二三')
  expect(input.value).toBe('')
  expect(seen.at(-1)).toBeNull()
 })

 it('adopts values pushed in from outside',()=>{
  const {rerender,getByRole}=render(<NumInput value={100} onValue={()=>{}}/>)
  const input=()=>getByRole('textbox') as HTMLInputElement
  expect(input().value).toBe('100')
  rerender(<NumInput value={200} onValue={()=>{}}/>)
  expect(input().value).toBe('200')
 })

 it('does not clobber the draft when the value it reported comes back',async()=>{
  // Typing "007" reports 7; the parent echoes 7 back. Resyncing on every
  // render would rewrite the field to "7" mid-edit and fight the operator.
  const user=userEvent.setup()
  const {input}=renderNum(null)
  await user.type(input,'007')
  expect(input.value).toBe('007')
 })
})

// QuotaFields is the form the operator actually fills in. These tests pin the
// key each field writes to and the tri-state value that reaches the wire.
function renderQuota(initial:Record<string,number|null>){
 const states:Record<string,any>[]=[]
 function Host(){
  const [v,setV]=useState<Record<string,any>>(initial)
  useEffect(()=>{states.push(v)},[v])
  return <QuotaFields v={v} setV={setV}/>
 }
 const utils=render(<Host/>)
 return {...utils,states}
}

describe('QuotaFields',()=>{
 // F wraps the input in a <label> that also carries the hint text, so the
 // accessible name is "每日 Token 上限留空=继承上级 · 0=不限额". Match on the
 // label part rather than the whole string.
 const DAILY=/每日 Token 上限/
 const MONTHLY=/每月 Token 上限/
 const REQUESTS=/每月请求上限/

 it('renders all four quota dimensions',()=>{
  const {getByLabelText}=renderQuota({})
  for(const label of [DAILY,MONTHLY,REQUESTS,/月预算 micros/]){
   expect(getByLabelText(label)).toBeTruthy()
  }
 })

 it('writes the monthly token ceiling to monthly_token_limit',async()=>{
  const user=userEvent.setup()
  const {getByLabelText,states}=renderQuota({})
  await user.type(getByLabelText(MONTHLY),'110')
  expect(states.at(-1)).toMatchObject({monthly_token_limit:110})
 })

 it('keeps the dimensions independent',async()=>{
  const user=userEvent.setup()
  const {getByLabelText,states}=renderQuota({})
  await user.type(getByLabelText(DAILY),'78')
  await user.type(getByLabelText(MONTHLY),'110')
  expect(states.at(-1)).toMatchObject({daily_token_limit:78,monthly_token_limit:110})
 })

 it('carries the inherit / unlimited distinction through the form',async()=>{
  const user=userEvent.setup()
  const {getByLabelText,states}=renderQuota({daily_token_limit:50,monthly_token_limit:100})
  await user.clear(getByLabelText(DAILY))
  expect(states.at(-1)).toMatchObject({daily_token_limit:null})
  // type() appends, so the field has to be emptied before "0" means 0.
  await user.clear(getByLabelText(MONTHLY))
  await user.type(getByLabelText(MONTHLY),'0')
  expect(states.at(-1)).toMatchObject({monthly_token_limit:0})
 })

 it('leaves untouched dimensions alone',async()=>{
  const user=userEvent.setup()
  const {getByLabelText,states}=renderQuota({monthly_request_limit:500})
  await user.type(getByLabelText(DAILY),'9')
  expect(states.at(-1)).toMatchObject({daily_token_limit:9,monthly_request_limit:500})
 })
})

// quotaLabel is what the operator actually reads, so its three states are worth
// pinning down: a value that is inherited and a value that is deliberately
// uncapped look identical if the rendering collapses them.
describe('quotaLabel',()=>{
 it('names the inherited state rather than showing a number',()=>{
  expect(quotaLabel(null)).toBe('继承上级')
  expect(quotaLabel(undefined)).toBe('继承上级')
 })

 it('does not render an explicit zero as the number zero',()=>{
  // 0 means "unlimited" throughout the quota model. Printing "0" here would
  // read as "you have no budget left", which is the opposite of the truth.
  expect(quotaLabel(0)).toBe('不限额')
 })

 it('formats a real limit with the injected formatter',()=>{
  expect(quotaLabel(5000)).toBe('5000')
  expect(quotaLabel(5000,n=>n.toLocaleString('en-US'))).toBe('5,000')
 })

 it('keeps inherit and unlimited distinct',()=>{
  expect(quotaLabel(null)).not.toBe(quotaLabel(0))
 })
})
