import {useState} from 'react'
import {afterEach, expect, test} from 'vitest'
import {cleanup, render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {LimitInput, limitClass, limitText} from './LimitInput'

afterEach(cleanup)

function Probe({initial = null as number | null}) {
  const [value, setValue] = useState<number | null>(initial)
  return <LimitInput value={value} onChange={setValue} />
}

test('a multi-digit cap can be typed without the box rewriting itself', async () => {
  const user = userEvent.setup()
  render(<Probe />)
  const input = screen.getByRole('textbox') as HTMLInputElement

  await user.click(input)
  await user.keyboard('1000000')

  expect(input.value).toBe('1000000')
})

test('the three states survive the round trip', async () => {
  const user = userEvent.setup()
  render(<Probe initial={5} />)
  const input = screen.getByRole('textbox') as HTMLInputElement

  await user.clear(input)
  expect(input.value).toBe('')

  await user.type(input, '0')
  expect(input.value).toBe('0')

  await user.clear(input)
  await user.type(input, '2500')
  expect(input.value).toBe('2500')
})

test('letters and signs are refused, not silently accepted', async () => {
  const user = userEvent.setup()
  render(<Probe />)
  const input = screen.getByRole('textbox') as HTMLInputElement

  await user.click(input)
  await user.keyboard('-12e3abc')

  expect(input.value).toBe('123')
})

test('inherit and unlimited do not render the same', () => {
  expect(limitText(null)).toBe('继承')
  expect(limitText(0)).toBe('不限')
  expect(limitText(1000)).not.toBe(limitText(null))
  expect(limitClass(null)).toBe('limitInherit')
  expect(limitClass(0)).toBe('limitUnlimited')
  expect(limitClass(1000)).toBe('limitSet')
})
