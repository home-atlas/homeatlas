// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { installNavigationFocus, returnToSection, scrollBehavior } from './navigation'
const create = () => createRouter({ history: createMemoryHistory(), routes: ['/', '/family', '/documents', '/documents/:id'].map(path => ({ path, component: { template: '<p />' } })) })
function moved(router: Router, action: () => void) { return new Promise<void>(resolve => { const stop = router.afterEach(() => { stop(); resolve() }); action() }) }
afterEach(() => { document.body.innerHTML = ''; vi.restoreAllMocks() })
it('returns to the previous library URL without adding a duplicate entry', async () => {
  const router = create(), library = '/documents?q=manual&category=manuales&offset=50&view=list'
  await router.push('/family'); await router.push(library); await router.push('/documents/a')
  router.options.history.replace('/documents/a', { back: library })
  await moved(router, () => returnToSection(router, '/documents'))
  expect(router.currentRoute.value.fullPath).toBe(library)
  await moved(router, () => router.back())
  expect(router.currentRoute.value.path).toBe('/family')
})
it('replaces a direct document link with the library instead of cycling back to the document', async () => {
  const router = create()
  await router.push('/family'); await router.push('/documents/a')
  await moved(router, () => returnToSection(router, '/documents'))
  expect(router.currentRoute.value.path).toBe('/documents')
  await moved(router, () => router.back())
  expect(router.currentRoute.value.path).toBe('/family')
})
it('restores saved scroll, handles real anchors and leaves query-only changes in place', async () => {
  const router = create()
  const route = (path: string) => ({ ...router.resolve(path), name: router.resolve(path).name ?? undefined })
  const home = route('/'), library = route('/documents')
  document.body.innerHTML = '<div id="home-search"></div>'
  expect(await scrollBehavior(library, home, { left: 0, top: 480 })).toEqual({ left: 0, top: 480 })
  expect(await scrollBehavior(library, home, null)).toEqual({ left: 0, top: 0 })
  expect(await scrollBehavior(route('/documents?view=list'), library, null)).toBe(false)
  expect(await scrollBehavior(route('/#home-search'), library, null)).toEqual({ el: document.getElementById('home-search') })
  expect(await scrollBehavior(route('/#%'), library, null)).toEqual({ left: 0, top: 0 })
})
it('moves keyboard focus without overriding scroll or stealing hash/query focus', async () => {
  const router = create()
  document.body.innerHTML = '<main id="main-content" tabindex="-1"></main>'
  const focus = vi.spyOn(document.getElementById('main-content')!, 'focus')
  const stop = installNavigationFocus(router)
  await router.push('/documents')
  expect(focus).toHaveBeenCalledWith({ preventScroll: true })
  focus.mockClear()
  await router.push('/documents?view=list'); await router.push('/#home-search')
  expect(focus).not.toHaveBeenCalled()
  stop()
})
