import {Detail, Progress} from '../ui/Detail'
import type {Tenant, TenantUsage} from '../../api'

/**
 * Usage drawer.
 *
 * Usage is fetched per tenant on demand rather than prefetching it for every
 * row: a page of twenty tenants would otherwise fire twenty extra requests
 * just to render a table.
 */
export function TenantUsageDetail({tenant, usage}: {tenant: Tenant; usage: TenantUsage}) {
  return (
    <div className="detailStack">
      <Detail
        title={tenant.name}
        rows={[
          ['租户编码', tenant.code],
          ['今日请求', usage.daily_requests],
          ['本月请求', usage.monthly_requests],
          ['今日 Tokens', usage.daily_tokens],
          ['本月 Tokens', usage.monthly_tokens],
        ]}
      />
      <section className="detailBox">
        <h3>配额使用情况</h3>
        <Progress label="每日 Token" used={usage.daily_tokens} limit={tenant.daily_token_limit} />
        <Progress label="每月 Token" used={usage.monthly_tokens} limit={tenant.monthly_token_limit} />
        <Progress label="每月请求" used={usage.monthly_requests} limit={tenant.monthly_request_limit} />
      </section>
    </div>
  )
}
