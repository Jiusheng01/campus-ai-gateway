import {useState} from 'react'
import {ApiKeysPage} from './ApiKeysPage'
import {TenantsPage} from './TenantsPage'

type Tab = 'keys' | 'tenants'

/**
 * 调用管理 composes the existing tenant and key pages instead of duplicating
 * their data services. The legacy page ids `keys` and `tenants` land on the
 * corresponding tab.
 */
export function CallsPage({initialTab = 'keys'}: {initialTab?: Tab}) {
  const [tab, setTab] = useState<Tab>(initialTab)
  return (
    <div className="stack">
      <div className="quickActions">
        <div className="segmented" role="group" aria-label="调用管理视图">
          <button
            type="button"
            className={`segmentedItem ${tab === 'keys' ? 'on' : ''}`}
            aria-pressed={tab === 'keys'}
            onClick={() => setTab('keys')}
          >
            API 密钥
          </button>
          <button
            type="button"
            className={`segmentedItem ${tab === 'tenants' ? 'on' : ''}`}
            aria-pressed={tab === 'tenants'}
            onClick={() => setTab('tenants')}
          >
            租户配额
          </button>
        </div>
      </div>
      {tab === 'keys' ? <ApiKeysPage /> : <TenantsPage />}
    </div>
  )
}
