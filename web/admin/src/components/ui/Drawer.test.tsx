import {useState} from 'react'
import {expect, test} from 'vitest'
import {cleanup, render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {Drawer} from './Drawer'

/**
 * The drawer must not take focus back while a field inside it is being typed
 * in.
 *
 * This is not a hypothetical: every caller passes `onClose={() => setX(null)}`,
 * a fresh function on each render, so an effect that depends on `onClose`
 * re-runs after every keystroke. It restores focus to the element that had it
 * before (the field) and then focuses the panel, which lands on the panel — the
 * field loses the caret and the next character goes nowhere. A cap of 1000000
 * could only be entered one digit per click.
 */
function Harness() {
  const [value, setValue] = useState('')
  const [, bump] = useState(0)
  return (
    <Drawer title="模型额度" onClose={() => bump(n => n + 1)}>
      <input aria-label="上限" value={value} onChange={e => setValue(e.target.value)} />
    </Drawer>
  )
}

test('typing in a field keeps the caret there', async () => {
  const user = userEvent.setup()
  render(<Harness />)
  const input = screen.getByLabelText('上限') as HTMLInputElement

  await user.click(input)
  await user.keyboard('1000000')

  expect(input.value).toBe('1000000')
  expect(document.activeElement).toBe(input)
})

test('Escape still closes the drawer after a re-render', async () => {
  const user = userEvent.setup()
  const closes: number[] = []
  function Probe() {
    const [n, setN] = useState(0)
    return (
      <Drawer title="t" onClose={() => {
        closes.push(n)
        setN(n + 1)
      }}>
        <input aria-label="字段" />
      </Drawer>
    )
  }
  render(<Probe />)
  await user.keyboard('{Escape}')
  expect(closes).toHaveLength(1)
  cleanup()
})
