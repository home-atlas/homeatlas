// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { getWebModule, type ModuleStatus } from '@/core/modules'
import type { HomeContribution, HomeItem } from '@/core/home'
import { modules } from '@/core/state'
import { setAuthState } from '@/core/auth'
import HomeView from './HomeView.vue'

vi.mock('@/core/modules', async original => ({ ...await original<typeof import('@/core/modules')>(), getWebModule: vi.fn() }))
const apps: { unmount(): void }[] = []
const status: ModuleStatus = { id: 'notes', name: 'Notas', version: '1', contractVersion: '1', available: true, enabled: true, accessPermission: 'notes.read', capabilities: ['notes.read'], dependencies: [], navigation: { label: 'Notas', path: '/notes' } }
const item: HomeItem = { id: 'one', title: 'Una nota privada', description: 'Información autorizada', to: '/notes/one' }
const contribution: HomeContribution = {
  caption: 'Tus notas', recentCaption: 'Última nota', recentTitle: 'Notas recientes', loadingText: 'Cargando notas…',
  icon: 'book', image: '/test-notes.png', tone: 'blue', collection: { label: 'Ver notas', to: '/notes' },
  empty: { title: 'Escribe tu primera nota', description: 'Un espacio para tus notas.', to: '/notes' },
  space: { description: 'Las notas de tu hogar.' }, suggestions: [{ label: 'Una nota importante', term: 'nota', icon: 'book', tone: 'blue' }],
  load: vi.fn(async () => [item]),
}
function login(permissions = ['notes.read'], role = 'member') {
  setAuthState({ setupRequired: false, session: { user: { id: 'member', name: 'Member', email: '' }, household: { id: 'home', name: 'Home', timezone: 'UTC' }, role, permissions, expiresAt: '2030-01-01T00:00:00Z' } })
}
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount() {
  const router = createRouter({ history: createMemoryHistory(), routes: ['/', '/family', '/settings', '/modules', '/notes', '/notes/:id'].map(path => ({ path, component: { template: '<p>Destino</p>' } })) })
  await router.push('/')
  const root = document.createElement('div'), app = createApp(HomeView).use(router)
  document.body.append(root); apps.push(app); app.mount(root); await flush(); return root
}
beforeEach(() => {
  login(); modules.value = [status]
  vi.mocked(contribution.load).mockResolvedValue([item])
  vi.mocked(getWebModule).mockImplementation((_router, id) => id === 'notes' ? { id, routes: [], home: contribution } : undefined)
})
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.resetAllMocks(); modules.value = []; setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })

it('renders a new module contribution, space and suggestions without a Documents branch', async () => {
  const root = await mount()
  expect(root.querySelector('a[href="/notes/one"]')?.textContent).toContain(item.title)
  expect(root.textContent).toContain('Las notas de tu hogar.')
  expect(root.textContent).toContain('Una nota importante')
  expect(root.querySelector('a[href="/documents"]')).toBeNull()
  expect(contribution.load).toHaveBeenCalledTimes(1)
})
it('handles empty data and rejects module results pointing outside their section', async () => {
  vi.mocked(contribution.load).mockResolvedValue([{ ...item, to: 'https://example.test' }, { ...item, to: '/notes/../settings' }, { ...item, to: '/documents/private' }])
  const root = await mount()
  expect(root.textContent).toContain('Escribe tu primera nota')
  expect(root.querySelector('a[href="/documents/private"]')).toBeNull()
  expect(root.querySelector('a[href="https://example.test"]')).toBeNull()
})
it('keeps module errors independent and supports retry', async () => {
  vi.mocked(contribution.load).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce([item])
  const root = await mount()
  expect(root.querySelector('[role=alert]')?.textContent).toContain('Inténtalo de nuevo')
  ;[...root.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Reintentar')!.click(); await flush()
  expect(root.querySelector('[role=alert]')).toBeNull()
  expect(root.textContent).toContain(item.title)
})
it('does not load a contribution without permission or with a disabled module', async () => {
  login([]); const root = await mount()
  expect(contribution.load).not.toHaveBeenCalled(); expect(root.textContent).not.toContain(item.title)
  login(); modules.value = [{ ...status, enabled: false }]; await flush()
  expect(root.querySelector('a[href="/notes"]')).toBeNull()
})
it('discards earlier module results after a role change and removes private content on logout', async () => {
  let old!: (items: HomeItem[]) => void
  vi.mocked(contribution.load).mockImplementationOnce(() => new Promise(resolve => { old = resolve }))
  const root = await mount()
  login(['notes.read'], 'restricted'); await flush()
  expect(root.textContent).toContain(item.title)
  old([{ ...item, title: 'Resultado de la sesión anterior' }]); await flush()
  expect(root.textContent).not.toContain('Resultado de la sesión anterior')
  setAuthState({ setupRequired: false, session: null }); await flush()
  expect(root.textContent).not.toContain(item.title)
})
