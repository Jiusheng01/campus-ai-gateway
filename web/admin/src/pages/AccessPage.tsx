import {api, type WhoAmI} from '../api'
import {Panel} from '../components/ui/Panel'
import {ErrorState} from '../components/ui/EmptyState'
import {SkeletonRows} from '../components/ui/Skeleton'
import {AccountSecurity} from '../auth/AccountSecurity'
import {useResource} from '../lib/useResource'

/**
 * Password and devices — the only credentials this console owns.
 *
 * The identity block is suppressed here: 个人资料 already shows who the account
 * is, and repeating the username and roles on a second page would let the two
 * disagree the moment one of them is refreshed and the other is not.
 */
export function AccessPage({who, onSignedOut}: {who: WhoAmI | null; onSignedOut: () => void}) {
  const {data, loading, error, errorStatus, reload} = useResource(() => api<WhoAmI>('/admin/whoami', {}, false))
  const identity = data || who

  return (
    <div className="stack">
      {error ? (
        <Panel title="安全与访问">
          <ErrorState message={error} status={errorStatus} onRetry={reload} />
        </Panel>
      ) : loading && !identity ? (
        <Panel title="安全与访问">
          <SkeletonRows rows={3} columns={2} />
        </Panel>
      ) : (
        <AccountSecurity who={identity} onSignedOut={onSignedOut} showIdentity={false} />
      )}
    </div>
  )
}
