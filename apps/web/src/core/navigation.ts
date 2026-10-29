import { nextTick } from 'vue'
import type { Router, RouterScrollBehavior } from 'vue-router'

export const scrollBehavior: RouterScrollBehavior = async (to, from, saved) => {
  await nextTick()
  if (saved) return saved
  if (to.hash) {
    let id = to.hash.slice(1)
    try { id = decodeURIComponent(id) } catch { /* Invalid fragments do not prevent navigation. */ }
    const target = document.getElementById(id)
    if (target) return { el: target }
  }
  if (to.path === from.path) return false
  return { left: 0, top: 0 }
}

export function installNavigationFocus(router: Router) {
  return router.afterEach(async (to, from, failure) => {
    if (failure || to.hash || to.path === from.path) return
    await nextTick()
    document.getElementById('main-content')?.focus({ preventScroll: true })
  })
}

export function returnToSection(router: Router, path: string) {
  const back = router.options.history.state.back
  if (typeof back === 'string' && back.startsWith('/') && !back.startsWith('//') && router.resolve(back).path === path) router.back()
  else void router.replace(path)
}
