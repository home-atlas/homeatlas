// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError, get } from '@/core/api'
import { setAuthState } from '@/core/auth'
import { modules } from '@/core/state'
import { registerModules, type ModuleStatus } from '@/core/modules'
import HomeView from './HomeView.vue'
vi.mock('@/core/api', async original => ({ ...await original<typeof import('@/core/api')>(), get: vi.fn() }))
const apps: { unmount: () => void }[] = []
const fixture: ModuleStatus = { id: 'documents', name: 'Documents', version: '1', contractVersion: '1', enabled: true, available: true, accessPermission: 'documents.read', capabilities: ['documents.read'], dependencies: [], navigation: { label: 'Documentos', path: '/documents' } }
const doc = { id: 'doc-1', title: 'Manual del refrigerador', category: 'manuales', createdAt: '2026-10-21T12:00:00Z' }
function login(permissions = ['documents.read']) {
  setAuthState({ setupRequired: false, session: { user: { id: 'alice', name: 'Alicia', email: '' }, household: { id: 'home-a', name: 'Casa', timezone: 'America/Mexico_City' }, role: 'restricted', permissions, expiresAt: '2026-10-22T00:00:00Z' } })
}
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount(path = '/') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: HomeView }, { path: '/documents/:id', component: { template: '<p>Detalle</p>' } }, { path: '/documents', component: { template: '<p>Biblioteca</p>' } }, { path: '/family', component: { template: '<p>Familia</p>' } }, { path: '/settings', component: { template: '<p>Ajustes</p>' } }, { path: '/modules', component: { template: '<p>Módulos</p>' } }] })
  await registerModules(router, modules.value)
  await router.push(path)
  const root = document.createElement('div'), app = createApp(HomeView).use(router)
  document.body.append(root); apps.push(app); app.mount(root); await flush()
  return { root, router }
}
async function search(root: HTMLElement, value: string) {
  const input = root.querySelector<HTMLInputElement>('#home-query')!; input.value = value; input.dispatchEvent(new Event('input', { bubbles: true })); await nextTick()
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
}
beforeEach(() => { login(); modules.value = [fixture]; vi.mocked(get).mockResolvedValue([]) })
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.resetAllMocks(); modules.value = []; setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })
it.each(['disabled', 'no permission', 'not installed'])('does not fetch or expose Documents when %s', async reason => {
  if (reason === 'disabled') modules.value = [{ ...fixture, enabled: false }]
  else if (reason === 'not installed') modules.value = []
  else login([])
  const { root } = await mount()
  expect(get).not.toHaveBeenCalled()
  expect(root.querySelector('a[href="/documents"]')).toBeNull()
  expect(root.querySelector<HTMLInputElement>('#home-query')!.disabled).toBe(true)
  expect(root.textContent).toContain('Todavía no tienes módulos disponibles')
  expect(root.textContent).not.toContain('Invita y ayuda')
})
it('links the latest authorized document and offers a useful empty state', async () => {
  vi.mocked(get).mockResolvedValue([doc])
  const { root } = await mount()
  expect(root.querySelector('a[href="/documents/doc-1"]')?.textContent).toContain(doc.title)
  expect(root.textContent).toContain('Manuales')
  expect(root.textContent).toContain('Tu familia, cerca')
  expect(root.textContent).not.toContain('garantía por vencer')
})
it('searches real metadata, encodes the query and filters unavailable module links', async () => {
  vi.mocked(get).mockResolvedValueOnce([]).mockResolvedValueOnce([{ module: 'documents', id: 'doc-1', title: 'Contrato & casa', path: '/documents/doc-1' }, { module: 'inventory', id: 'hidden', title: 'Oculto', path: '/inventory/hidden' }, { module: 'documents', id: 'external', title: 'Enlace ajeno', path: 'https://example.test' }])
  const { root } = await mount(); await search(root, 'contrato & casa')
  expect(get).toHaveBeenLastCalledWith('/api/search?q=contrato%20%26%20casa')
  expect(root.querySelector('a[href="/documents/doc-1"]')?.textContent).toContain('Contrato & casa')
  expect(root.textContent).not.toContain('Oculto'); expect(root.textContent).not.toContain('Enlace ajeno')
})
it('rejects UTF-8 queries over the server limit without sending them', async () => {
  const { root } = await mount(); await search(root, 'á'.repeat(101))
  expect(root.querySelector('[role=alert]')?.textContent).toContain('muy larga')
  expect(get).toHaveBeenCalledTimes(1)
})
it('ignores old search responses when the words change', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(get).mockResolvedValueOnce([]).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const { root } = await mount(); await search(root, 'manual')
  const input = root.querySelector<HTMLInputElement>('#home-query')!; input.value = 'otro'; input.dispatchEvent(new Event('input', { bubbles: true }))
  resolve([{ module: 'documents', id: 'old', title: 'Resultado antiguo', path: '/documents/old' }]); await flush()
  expect(root.textContent).not.toContain('Resultado antiguo')
  expect(root.textContent).not.toContain('Resultados de tu búsqueda')
})
it('clears private content and discards pending requests after logout', async () => {
  let resolveDocs!: (value: unknown) => void, resolveSearch!: (value: unknown) => void
  vi.mocked(get).mockImplementationOnce(() => new Promise(done => { resolveDocs = done })).mockImplementationOnce(() => new Promise(done => { resolveSearch = done }))
  const { root } = await mount(); await search(root, 'manual')
  setAuthState({ setupRequired: false, session: null })
  resolveDocs([doc]); resolveSearch([{ module: 'documents', id: 'old', title: 'Privado', path: '/documents/old' }]); await flush()
  expect(root.textContent).not.toContain(doc.title); expect(root.textContent).not.toContain('Privado')
  expect(root.querySelector('a[href="/documents/doc-1"]')).toBeNull()
})
it('explains empty results and allows retry after an error', async () => {
  vi.mocked(get).mockResolvedValueOnce([]).mockRejectedValueOnce(new ApiError(503)).mockResolvedValueOnce([])
  const { root } = await mount(); await search(root, 'manual')
  expect(root.querySelector('[role=alert]')?.textContent).toContain('Inténtalo de nuevo')
  await search(root, 'manual')
  expect(root.textContent).toContain('No encontramos resultados')
  expect(root.querySelector('[role=alert]')).toBeNull()
})
it('focuses the search field when opened from navigation', async () => {
  const { root } = await mount('/#home-search')
  expect(document.activeElement).toBe(root.querySelector('#home-query'))
})

it('restores submitted home searches from their URL and through back/forward', async () => {
  const { root, router } = await mount('/?q=manual#home-search')
  expect(root.querySelector<HTMLInputElement>('#home-query')!.value).toBe('manual')
  expect(get).toHaveBeenLastCalledWith('/api/search?q=manual')
  await search(root, 'contrato')
  expect(router.currentRoute.value.query.q).toBe('contrato')
  router.back(); await flush()
  expect(root.querySelector<HTMLInputElement>('#home-query')!.value).toBe('manual')
  expect(get).toHaveBeenLastCalledWith('/api/search?q=manual')
  router.forward(); await flush()
  expect(root.querySelector<HTMLInputElement>('#home-query')!.value).toBe('contrato')
})
it('removes a closed search from the URL so reload does not reopen it', async () => {
  const { root, router } = await mount('/?q=manual#home-search')
  ;[...root.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.includes('Cerrar búsqueda'))!.click()
  await flush()
  expect(router.currentRoute.value.query.q).toBeUndefined()
  expect(root.textContent).not.toContain('Resultados de tu búsqueda')
  expect(root.querySelector<HTMLInputElement>('#home-query')!.value).toBe('')
})
