/** Keep Tab within a modal surface, skipping controls hidden by responsive CSS. */
export function containTab(event: KeyboardEvent, panel: HTMLElement) {
  if (event.key !== 'Tab') return
  const controls = Array.from(panel.querySelectorAll<HTMLElement>(
    'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
  )).filter(element => {
    for (let node: HTMLElement | null = element; node; node = node.parentElement) {
      const style = window.getComputedStyle(node)
      if (node.hidden || node.getAttribute('aria-hidden') === 'true' || style.display === 'none' || style.visibility === 'hidden') return false
      if (node === panel) break
    }
    return true
  })
  const first = controls[0]
  const last = controls[controls.length - 1]
  const active = document.activeElement
  if (!first || !last) {
    event.preventDefault()
    panel.focus()
  } else if (!active || !panel.contains(active) || active === panel) {
    event.preventDefault()
    ;(event.shiftKey ? last : first).focus()
  } else if (event.shiftKey ? active === first : active === last) {
    event.preventDefault()
    ;(event.shiftKey ? last : first).focus()
  }
}
