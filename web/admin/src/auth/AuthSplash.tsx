import {Loader2} from 'lucide-react'

export function AuthSplash({text}: {text: string}) {
  return (
    <div className="authPage">
      <div className="authCard authCenter">
        <div className="brandMark lg" aria-hidden>
          AI
        </div>
        <p className="authHint">
          <Loader2 className="spin" aria-hidden /> {text}
        </p>
      </div>
    </div>
  )
}
