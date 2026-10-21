import {describe, expect, it} from 'vitest'
import {render, screen} from '@testing-library/react'
import {Progress} from './Detail'

/**
 * A tenant limit has three states, and the progress row must keep them apart:
 * zero is a configured limit that is deliberately not enforced (无限制), while
 * an absent value means no limit was configured (未设置). Rendering both as
 * "不限" would claim a policy the gateway never had.
 */
describe('Progress', () => {
  it('renders a configured zero as 无限制', () => {
    render(<Progress label="每日 Token" used={0} limit={0} />)
    expect(screen.getByText(/0 \/ 无限制/)).toBeInTheDocument()
  })

  it('renders an absent limit as 未设置', () => {
    render(<Progress label="每日 Token" used={0} limit={undefined} />)
    expect(screen.getByText(/0 \/ 未设置/)).toBeInTheDocument()
  })

  it('renders a configured limit as the number', () => {
    render(<Progress label="每日 Token" used={250} limit={1000} />)
    expect(screen.getByText('250 / 1,000')).toBeInTheDocument()
  })
})
