// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAuthState } from '@/core/auth'
import { modules } from '@/core/state'
import { ApiError, get, post } from '@/core/api'
import PluginView from './PluginView.vue'

vi.mock('@/core/api', async original => ({ ...await original<typeof import('@/core/api')>(), get: vi.fn(), post: vi.fn() }))
const apps: { unmount(): void }[] = []
const manifest = { id: 'ext-tools', name: 'Herramientas', version: '1', contractVersion: '1', views: [
  { id: 'temperature', title: 'Temperatura', description: 'Conversión', submitLabel: 'Convertir', fields: [{ name: 'value', label: 'Grados', type: 'number', required: true }] },
  { id: 'energy', title: 'Energía', description: 'Consumo', submitLabel: 'Calcular', fields: [] },
] }
function login(permissions = ['ext-tools.read', 'ext-tools.write'], user = 'a') {
  setAuthState({ setupRequired: false, session: { user: { id: user, name: 'User', email: '' }, household: { id: 'home', name: 'Home', timezone: 'UTC' }, role: 'member', permissions, expiresAt: '' } })
}
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:all(.*)*', component: { template: '<p>Route</p>' } }] })
  await router.push('/ext-tools')
  const root = document.createElement('div'); document.body.append(root)
  const app = createApp(PluginView, { pluginId: 'ext-tools' }).use(router); apps.push(app); app.mount(root); await flush(); return root
}
beforeEach(() => {
  login(); modules.value = [{ id: 'ext-tools', name: 'Tools', version: '1', contractVersion: '1', runtime: 'wasi-preview1', enabled: true, available: true, accessPermission: 'ext-tools.read', capabilities: ['ext-tools.read', 'ext-tools.write'], dependencies: [], navigation: { label: 'Tools', path: '/ext-tools' } }]
  vi.mocked(get).mockResolvedValue(manifest); vi.mocked(post).mockResolvedValue({ text: '<img src=x onerror=alert(1)> Resultado' })
})
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.resetAllMocks(); modules.value = []; setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })

it('renders plugin views with shared fields, submits JSON and escapes guest output', async () => {
  const root = await mount()
  expect(root.querySelector('a[href="/ext-tools/energy"]')?.textContent).toContain('Energía')
  const input = root.querySelector<HTMLInputElement>('input')!; expect(input.id).toBe('ext-tools-value'); expect(input.required).toBe(true)
  input.value = '25'; input.dispatchEvent(new Event('input', { bubbles: true })); await flush()
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
  expect(post).toHaveBeenCalledWith('/api/ext-tools/actions/temperature', { value: '25' }, 10000)
  expect(root.querySelector('[role=status]')?.textContent).toContain('<img'); expect(root.querySelector('img')).toBeNull()
})
it('honors execution permissions and invalidates data when permissions or account change', async () => {
  login(['ext-tools.read']); const root = await mount()
  expect(root.querySelector<HTMLButtonElement>('button[type=submit]')!.disabled).toBe(true)
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush(); expect(post).not.toHaveBeenCalled()
  login([]); await flush(); expect(root.querySelector('form')).toBeNull(); expect(root.textContent).not.toContain('Conversión')
})
it('allows a read-only plugin view with the existing read permission and never enables writes', async () => {
  login(['ext-tools.read'])
  vi.mocked(get).mockResolvedValue({ ...manifest, storage: 'user', views: [{ ...manifest.views[0], readOnly: true }] })
  const root = await mount()
  expect(root.querySelector<HTMLButtonElement>('button[type=submit]')!.disabled).toBe(false)
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
  expect(post).toHaveBeenCalledTimes(1)
  vi.mocked(get).mockResolvedValue({ ...manifest, storage: 'user' })
  login(['ext-tools.read'], 'other'); await flush()
  expect(root.querySelector<HTMLButtonElement>('button[type=submit]')!.disabled).toBe(true)
})
it('uses the share capability for sharing views without requiring edit permission', async () => {
  login(['ext-tools.read', 'ext-tools.share'])
  vi.mocked(get).mockResolvedValue({ ...manifest, storage: 'shared', views: [{ ...manifest.views[0], permission: 'share' }] })
  const root = await mount()
  expect(root.querySelector<HTMLButtonElement>('button[type=submit]')!.disabled).toBe(false)
  login(['ext-tools.read'], 'other'); await flush()
  expect(root.querySelector<HTMLButtonElement>('button[type=submit]')!.disabled).toBe(true)
})
it('discards in-flight output after logout', async () => {
  let resolve!: (value: { text: string }) => void
  vi.mocked(post).mockImplementation(() => new Promise(done => { resolve = done }))
  const root = await mount(); root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
  setAuthState({ setupRequired: false, session: null }); await flush(); resolve({ text: 'Resultado privado' }); await flush()
  expect(root.textContent).not.toContain('Resultado privado'); expect(root.querySelector('form')).toBeNull()
})
it('offers retry after load failure and reports runtime admission failures', async () => {
  vi.mocked(get).mockRejectedValueOnce(new Error('offline'))
  const root = await mount(); expect(root.querySelector('[role=alert]')).not.toBeNull()
  ;[...root.querySelectorAll('button')].find(b => b.textContent === 'Reintentar')!.click(); await flush()
  vi.mocked(post).mockRejectedValueOnce(new ApiError(429, 'plugin_busy'))
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
  expect(root.querySelector('[role=alert]')?.textContent).toContain('Hay otra acción')
})

it('renders declarative multiline notes and submits their line breaks without HTML interpretation', async () => {
  vi.mocked(get).mockResolvedValue({ ...manifest, views: [{ ...manifest.views[0], fields: [{ name: 'note', label: 'Tu nota', type: 'textarea', required: true }] }] })
  const root = await mount()
  const field = root.querySelector<HTMLTextAreaElement>('textarea')!
  expect(field.required).toBe(true)
  expect(field.maxLength).toBe(2000)
  field.value = 'Pan\nLeche'
  field.dispatchEvent(new Event('input', { bubbles: true })); await flush()
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
  expect(post).toHaveBeenCalledWith('/api/ext-tools/actions/temperature', { note: 'Pan\nLeche' }, 10000)
  login(['ext-tools.read'], 'other'); await flush()
  expect(root.querySelector<HTMLTextAreaElement>('textarea')!.disabled).toBe(true)
})
