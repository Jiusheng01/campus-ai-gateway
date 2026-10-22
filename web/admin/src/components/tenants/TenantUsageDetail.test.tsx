import {describe, expect, it} from 'vitest'
import {render, screen, within} from '@testing-library/react'
import {TenantUsageDetail} from './TenantUsageDetail'
import type {Tenant, TenantUsage} from '../../api'

function tenant(over: Partial<Tenant> = {}): Tenant {
  return {
    id: 't-1',
    code: 'cs-dept',
    name: '计算机学院',
    status: 1,
    daily_token_limit: 1000,
    monthly_token_limit: 0,
    monthly_request_limit: 500,
    monthly_budget_micros: 0,
    rpm_limit: 0,
    tpm_limit: 0,
    max_concurrency: 0,
    allowed_models: ['*'],
    ...over,
  }
}

const usage: TenantUsage = {
  tenant_id: 't-1',
  daily_requests: 12,
  monthly_requests: 640,
  daily_tokens: 1200,
  monthly_tokens: 12000,
}

describe('TenantUsageDetail quota bands', () => {
  // The bar is capped at 100% by geometry, so it cannot by itself say how far
  // past the limit a tenant is. The text has to carry the actual overage.
  it('states the overage and the real percentage when usage exceeds the limit', () => {
    render(<TenantUsageDetail tenant={tenant()} usage={usage} />)

    const band = screen.getByText('每日 Token').closest('.progress') as HTMLElement
    expect(band).toHaveTextContent('1,200')
    expect(band).toHaveTextContent('/ 1,000')
    expect(within(band).getByText(/已超出 200（120%）/)).toBeInTheDocument()
  })

  // Zero is a configured "no limit", not a 0% budget. Dividing by it would
  // state a proportion the gateway never enforces.
  it('does not quote a percentage for an unlimited band', () => {
    render(<TenantUsageDetail tenant={tenant()} usage={usage} />)

    const unlimited = screen.getByText('每月 Token').closest('.progress') as HTMLElement
    expect(unlimited).toHaveTextContent('/ 无限制')
    expect(within(unlimited).getByText(/不计算占比/)).toBeInTheDocument()
    expect(within(unlimited).queryByText(/%/)).not.toBeInTheDocument()
  })

  it('labels an unset limit as 未设置 rather than zero', () => {
    const {daily_token_limit: _omit, ...rest} = tenant()
    render(<TenantUsageDetail tenant={rest as Tenant} usage={usage} />)

    const band = screen.getByText('每日 Token').closest('.progress') as HTMLElement
    expect(band).toHaveTextContent('/ 未设置')
    expect(within(band).getByText(/不计算占比/)).toBeInTheDocument()
    expect(within(band).queryByText(/%/)).not.toBeInTheDocument()
  })

  it('states the overage for the request band', () => {
    render(<TenantUsageDetail tenant={tenant()} usage={usage} />)

    const band = screen.getByText('每月请求').closest('.progress') as HTMLElement
    // 640 of 500 is 128%, so the text keeps the whole percentage while the bar
    // stops at full width.
    expect(band).toHaveTextContent('640')
    expect(band).toHaveTextContent('/ 500')
    expect(within(band).getByText(/已超出 140（128%）/)).toBeInTheDocument()
  })
})

describe('TenantUsageDetail rounding', () => {
  // 40 of 2,000,000 is a real use. Rounding it to 0% beside a non-zero
  // numerator would say two contradictory things at once.
  it('does not round a small but real share down to zero', () => {
    render(<TenantUsageDetail tenant={tenant({daily_token_limit: 2_000_000})} usage={{...usage, daily_tokens: 40}} />)
    const band = screen.getByText('每日 Token').closest('.progress') as HTMLElement
    expect(within(band).getByText(/已用 <0\.1%/)).toBeInTheDocument()
    expect(within(band).queryByText(/已用 0%/)).not.toBeInTheDocument()
  })
})

describe('TenantUsageDetail period', () => {
  // Quota windows are enforced in UTC by the gateway. A reader comparing the
  // drawer with a local-time dashboard would otherwise think one of them is
  // wrong.
  it('names the UTC day and month the counts are taken over', () => {
    render(<TenantUsageDetail tenant={tenant()} usage={usage} />)
    expect(screen.getByText(/UTC/)).toBeInTheDocument()
    expect(screen.getByText(/自然日/)).toBeInTheDocument()
    expect(screen.getByText(/自然月/)).toBeInTheDocument()
  })

  it('says when a window has no usage instead of drawing a zero bar', () => {
    render(<TenantUsageDetail tenant={tenant()} usage={{...usage, daily_requests: 0, daily_tokens: 0}} />)
    const band = screen.getByText('每日 Token').closest('.progress') as HTMLElement
    expect(within(band).getByText(/本周期暂无使用/)).toBeInTheDocument()
  })
})
