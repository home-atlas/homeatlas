// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { get } from '@/core/api'
import { setAuthState } from '@/core/auth'
import { modules } from '@/core/state'
import type { ModuleStatus } from '@/core/modules'
import SearchView from './SearchView.vue'
vi.mock('@/core/api', async original => ({ ...await original<typeof import('@/core/api')>(), get: vi.fn() }))
const fixture: ModuleStatus = { id: 'documents', name: 'Documents', version: '1', contractVersion: '1', enabled: true, available: true, accessPermission: 'documents.read', capabilities: ['documents.read'], dependencies: [], navigation: { label: 'Documentos', path: '/documents' } }
const apps: { unmount(): void }[] = []
function login(id = 'alice', permissions = ['documents.read'], sessionID = 'session-a') { setAuthState({ setupRequired: false, session: { id: sessionID, user: { id, name: id, email: '' }, household: { id: 'home', name: 'Casa', timezone: 'UTC' }, role: 'member', permissions, expiresAt: '2027-01-01T00:00:00Z' } }) }
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount(path = '/search') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/search', component: SearchView }, { path: '/documents/:id', component: { template: '<p>Detalle</p>' } }, { path: '/modules', component: { template: '<p>Módulos</p>' } }] })
  await router.push(path)
  const root = document.createElement('div'), app = createApp(SearchView).use(router); apps.push(app); document.body.append(root); app.mount(root); await flush()
  return { root, router }
}
beforeEach(() => { login(); modules.value = [fixture]; vi.mocked(get).mockResolvedValue([]) })
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.resetAllMocks(); modules.value = []; setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })
it('keeps the query/module in the URL and renders only authorized local links', async () => {
  vi.mocked(get).mockResolvedValue([{ module: 'documents', id: 'one', title: 'Manual privado', path: '/documents/one' }, { module: 'documents', id: 'remote', title: 'Enlace ajeno', path: 'https://example.test' }, { module: 'inventory', id: 'hidden', title: 'Oculto', path: '/inventory/hidden' }])
  const { root, router } = await mount('/search?q=manual+privado&module=documents')
  expect(get).toHaveBeenCalledWith('/api/search?q=manual+privado&module=documents')
  expect(root.querySelector('a[href="/documents/one"]')?.textContent).toContain('Manual privado')
  expect(root.textContent).not.toContain('Enlace ajeno'); expect(root.textContent).not.toContain('Oculto')
  expect(router.currentRoute.value.query.module).toBe('documents')
})
it('has a useful empty state with zero installed/authorized modules', async () => {
  modules.value = []
  const { root } = await mount('/search?q=manual')
  expect(get).not.toHaveBeenCalled(); expect(root.textContent).toContain('Todavía no tienes módulos disponibles')
  expect(root.querySelector<HTMLInputElement>('#global-query')?.disabled).toBe(true)
})
it('does not query an unavailable module or an oversized UTF-8 term', async () => {
  const { root, router } = await mount('/search?q=manual&module=unavailable')
  expect(get).not.toHaveBeenCalled(); expect(root.querySelector('[role=alert]')?.textContent).toContain('no está disponible')
  await router.push('/search?q=' + 'á'.repeat(101)); await flush()
  expect(get).not.toHaveBeenCalled(); expect(root.querySelector('[role=alert]')?.textContent).toContain('muy larga')
})
it('ignores a pending result after the draft or account changes', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(get).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const { root } = await mount('/search?q=manual')
  const field = root.querySelector<HTMLInputElement>('#global-query')!; field.value = 'otra cosa'; field.dispatchEvent(new Event('input', { bubbles: true })); await nextTick()
  resolve([{ module: 'documents', id: 'old', title: 'Resultado anterior', path: '/documents/old' }]); await flush()
  expect(root.textContent).not.toContain('Resultado anterior')
  vi.mocked(get).mockResolvedValue([{ module: 'documents', id: 'current', title: 'Actual', path: '/documents/current' }])
  login('bob', []); await flush()
  expect(root.textContent).not.toContain('Actual'); expect(root.textContent).not.toContain('Resultado anterior')
})
it('clears private results and discards responses after logout', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(get).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const { root } = await mount('/search?q=manual')
  setAuthState({ setupRequired: false, session: null }); await nextTick()
  resolve([{ module: 'documents', id: 'old', title: 'Privado de Alicia', path: '/documents/old' }]); await flush()
  expect(root.textContent).not.toContain('Privado de Alicia')
})
it('discards responses from an older login even for the same account and permissions', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(get).mockImplementationOnce(() => new Promise(done => { resolve = done })).mockResolvedValue([])
  const { root } = await mount('/search?q=manual')
  login('alice', ['documents.read'], 'session-b'); await flush()
  resolve([{ module: 'documents', id: 'old', title: 'Respuesta del acceso anterior', path: '/documents/old' }]); await flush()
  expect(root.textContent).not.toContain('Respuesta del acceso anterior')
})
