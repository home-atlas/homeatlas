// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'
import { createAppRouter } from './router'
import { get } from './core/api'
import { setAuthState, type AuthState } from './core/auth'
import { modules, refreshModules } from './core/state'
import type { ModuleStatus } from './core/modules'
vi.mock('./core/api', async original => ({ ...await original<typeof import('./core/api')>(), get: vi.fn() }))
const signedIn: AuthState = { setupRequired: false, session: { user: { id: 'a', name: 'Ana', email: '' }, household: { id: 'h', name: 'Casa', timezone: 'UTC' }, role: 'owner', permissions: ['documents.read'], expiresAt: '2026-10-22T00:00:00Z' } }
const fixture: ModuleStatus = { id: 'documents', name: 'Documents', version: '1', contractVersion: '1', enabled: true, available: true, accessPermission: 'documents.read', capabilities: ['documents.read'], dependencies: [], navigation: { label: 'Documentos', path: '/documents' } }
let state: AuthState, catalog: ModuleStatus[]
beforeEach(() => { state = structuredClone(signedIn); catalog = [fixture]; setAuthState(state); vi.mocked(get).mockImplementation(async path => path === '/api/auth/session' ? state : catalog); vi.spyOn(window, 'scrollTo').mockImplementation(() => {}) })
afterEach(() => { vi.restoreAllMocks(); vi.resetAllMocks(); setAuthState({ setupRequired: false, session: null }); modules.value = [] })
it('resolves a cold deep link after registering Documents', async () => {
  const router = createAppRouter(createMemoryHistory())
  await router.push('/documents/a')
  expect(router.currentRoute.value.name).toBe('document-detail')
  expect(router.currentRoute.value.params.id).toBe('a')
})
it('preserves the full private destination through login and replaces the authentication entry', async () => {
  state = { setupRequired: false, session: null }
  const router = createAppRouter(createMemoryHistory())
  await router.push('/documents?category=manuales&view=list')
  expect(router.currentRoute.value.name).toBe('login')
  expect(router.currentRoute.value.query.redirect).toBe('/documents?category=manuales&view=list')
  state = structuredClone(signedIn)
  await router.replace(router.currentRoute.value.fullPath + '&method=password')
  expect(router.currentRoute.value.fullPath).toBe('/documents?category=manuales&view=list')
})
it('keeps disabled modules and readers without permission out of document routes', async () => {
  const router = createAppRouter(createMemoryHistory())
  catalog = [{ ...fixture, enabled: false }]
  await router.push('/documents/a')
  expect(router.currentRoute.value.path).toBe('/modules')
  catalog = [fixture]; state = { ...signedIn, session: { ...signedIn.session!, permissions: [] } }
  await router.push('/documents/a')
  expect(router.currentRoute.value.path).toBe('/modules')
})
it('keeps compatibility links and their private fragment without introducing an intermediate route', async () => {
  state = { setupRequired: false, session: null }
  const router = createAppRouter(createMemoryHistory())
  await router.push('/recover?source=local#example-proof')
  expect(router.currentRoute.value.fullPath).toBe('/forgot-password?source=local#example-proof')
})

it.each(['back', 'forward'] as const)('does not cancel one %s press when a focus refresh overlaps the module check', async direction => {
  const router = createAppRouter(createMemoryHistory())
  await router.push('/documents'); await router.push('/documents/a')
  if (direction === 'forward') {
    const previous = new Promise<void>(resolve => { const stop = router.afterEach(() => { stop(); resolve() }) })
    router.back(); await previous
  }
  let finish!: (value: ModuleStatus[]) => void
  const pending = new Promise<ModuleStatus[]>(resolve => { finish = resolve })
  vi.mocked(get).mockImplementation(async path => path === '/api/auth/session' ? state : pending)
  const returned = new Promise<unknown>(resolve => { const stop = router.afterEach((_to, _from, failure) => { stop(); resolve(failure) }) })
  router[direction]()
  await new Promise(resolve => setTimeout(resolve, 0))
  const focusRefresh = refreshModules(router)
  finish([fixture])
  await focusRefresh
  expect(await returned).toBeUndefined()
  expect(router.currentRoute.value.path).toBe(direction === 'back' ? '/documents' : '/documents/a')
})
