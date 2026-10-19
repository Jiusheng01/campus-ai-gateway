import {Detail, Progress} from '../ui/Detail'
import type {EffectiveLimits, Tenant, TenantUsage} from '../../api'
import {limitText} from './LimitInput'

/**
 * Usage drawer.
 *
 * Usage is fetched per tenant on demand rather than prefetching it for every
 * row: a page of twenty tenants would otherwise fire twenty extra requests
 * just to render a table.
 *
 * The bars are drawn against the EFFECTIVE limit, not the tenant's own
 * declaration. Under a hierarchy most tenants declare nothing and inherit from
 * an ancestor, so a bar built from the declared column would sit at zero for a
 * tenant that is in fact capped by its school.
 */
export function TenantUsageDetail({tenant, usage}: {tenant: Tenant; usage: TenantUsage}) {
  const eff: EffectiveLimits | undefined = usage.effective
  return (
    <div className="detailStack">
      <Detail
        title={tenant.name}
        rows={[
          ['租户编码', tenant.code],
          ['层级', tenant.depth === undefined ? '—' : `第 ${tenant.depth + 1} 层`],
          ['今日请求', usage.daily_requests],
          ['本月请求', usage.monthly_requests],
          ['今日 Tokens', usage.daily_tokens],
          ['本月 Tokens', usage.monthly_tokens],
        ]}
      />
      <section className="detailBox">
        <h3>配额使用情况</h3>
        <p className="fieldHint">分母为沿层级继承后实际生效的上限。</p>
        <Progress label="每日 Token" used={usage.daily_tokens} limit={eff ? eff.daily_token_limit : tenant.daily_token_limit} />
        <Progress label="每月 Token" used={usage.monthly_tokens} limit={eff ? eff.monthly_token_limit : tenant.monthly_token_limit} />
        <Progress label="每月请求" used={usage.monthly_requests} limit={eff ? eff.monthly_request_limit : tenant.monthly_request_limit} />
      </section>
      <section className="detailBox">
        <h3>本级声明</h3>
        <p className="fieldHint">这一行写的是本租户自己设的值，留空即继承上级。</p>
        <Detail
          title=""
          rows={[
            ['每日 Token', limitText(tenant.daily_token_limit)],
            ['每月 Token', limitText(tenant.monthly_token_limit)],
            ['每月请求', limitText(tenant.monthly_request_limit)],
            ['RPM', limitText(tenant.rpm_limit)],
            ['TPM', limitText(tenant.tpm_limit)],
            ['最大并发', limitText(tenant.max_concurrency)],
          ]}
        />
      </section>
    </div>
  )
}
