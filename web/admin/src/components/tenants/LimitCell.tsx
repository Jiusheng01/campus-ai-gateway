import {limitClass, limitText} from './LimitInput'

/**
 * One model-quota cell: what this subject declares, and what the limiter will
 * apply. The second line is omitted when it would only repeat the first (an own
 * cap no ancestor tightens). When an ancestor is capping it, the source is
 * named -- that is the answer to "I set 300, why does it stop at 100?".
 */
export function LimitCell({
  configured,
  effective,
  setBy,
}: {
  configured: number | null
  effective?: number
  setBy?: string
}) {
  const redundant = configured !== null && configured > 0 && effective === configured
  const uncapped = configured === 0 && effective === 0
  let note = ''
  if (effective !== undefined && !redundant && !uncapped && setBy) {
    if (configured === null) note = `继承自 ${setBy}`
    else if (effective > 0 && (configured === 0 || effective < configured)) note = `受 ${setBy} 限制`
  }
  return (
    <div className="quotaEffectiveCell">
      <span className={limitClass(configured)}>{limitText(configured)}</span>
      {!redundant && !uncapped && effective !== undefined && (
        <small className="quotaEffective">
          生效 {limitText(effective)}
          {note && <span className="quotaSource">{note}</span>}
        </small>
      )}
    </div>
  )
}
