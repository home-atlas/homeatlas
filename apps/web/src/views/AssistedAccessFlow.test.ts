// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError, post } from '@/core/api'
import { setAuthState } from '@/core/auth'
import ActivateAccessView from './ActivateAccessView.vue'

vi.mock('@/core/api', async importOriginal => ({ ...await importOriginal<typeof import('@/core/api')>(), post: vi.fn() }))
const apps: { unmount: () => void }[] = []
function nodes(root: HTMLElement): HTMLElement[] { return [root, ...root.querySelectorAll<HTMLElement>('*')] }
function text(root: HTMLElement): string { return root.textContent ?? '' }
function byId(root: HTMLElement, id: string) { const found = root.querySelector<HTMLInputElement>('#' + id); if (!found) throw new Error('Missing ' + id); return found }
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount(signedIn = false) {
  setAuthState({ setupRequired: false, session: signedIn ? { user: { id: 'owner', name: 'Persona que ayuda', email: '' }, household: { id: 'home', name: 'Casa', timezone: 'UTC' }, role: 'owner', permissions: [], expiresAt: '' } : null })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/activate-access', component: ActivateAccessView }, { path: '/login', component: { template: '<p>Login</p>' } }] })
  await router.push('/activate-access#test-private-token')
  const root = document.createElement('div'), app = createApp(ActivateAccessView).use(router)
  document.body.append(root)
  apps.push(app); app.mount(root); await flush()
  return { root, router }
}
function fill(root: HTMLElement, id: string, value: string) { const input = byId(root, id); input.value = value; input.dispatchEvent(new Event('input', { bubbles: true })) }
async function submit(root: HTMLElement) { root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush() }
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.resetAllMocks(); setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })
it('guides two steps, focuses the next field, catches mismatch, and only sends the confirmed PIN', async () => {
  vi.mocked(post).mockResolvedValueOnce({ name: 'María', username: 'maria' }).mockResolvedValueOnce(undefined)
  const { root, router } = await mount()
  expect(router.currentRoute.value.hash).toBe('')
  expect(text(root)).toContain('Paso 1 de 2')
  expect(nodes(root).some(n => n.id === 'activation-confirm')).toBe(false)
  fill(root, 'activation-pin', '642817'); await submit(root)
  expect(text(root)).toContain('Paso 2 de 2'); expect(document.activeElement?.id).toBe('activation-confirm')
  expect(vi.mocked(post)).toHaveBeenCalledTimes(1)
  fill(root, 'activation-confirm', '111111'); await submit(root)
  expect(text(root)).toContain('Los números no coinciden')
  expect(byId(root, 'activation-confirm').getAttribute('aria-invalid')).toBe('true')
  expect(vi.mocked(post)).toHaveBeenCalledTimes(1)
  fill(root, 'activation-confirm', '642817'); await submit(root)
  expect(post).toHaveBeenLastCalledWith('/api/assisted/complete', { token: 'test-private-token', pin: '642817', deviceName: 'Mi teléfono o computadora' })
  expect(text(root)).toContain('Tu PIN está listo'); expect(text(root)).toContain('No necesitas crear otra cuenta')
  expect(nodes(root).some(n => n.tagName === 'INPUT')).toBe(false)
})
it('allows changing a PIN without submitting and resets confirmation', async () => {
  vi.mocked(post).mockResolvedValue({ name: 'María', username: 'maria' })
  const { root } = await mount()
  fill(root, 'activation-pin', '642817'); await submit(root)
  fill(root, 'activation-confirm', '111111')
  nodes(root).find(n => n.tagName === 'BUTTON' && text(n).includes('Volver y cambiar'))!.click(); await flush()
  expect(document.activeElement?.id).toBe('activation-pin')
  await submit(root)
  expect(byId(root, 'activation-confirm').value).toBe('')
  expect(post).toHaveBeenCalledTimes(1)
})
it('explains an expired link without displaying PIN creation', async () => {
  vi.mocked(post).mockRejectedValue(new ApiError(410, 'enrollment_unavailable'))
  const { root } = await mount()
  expect(text(root)).toContain('Pide uno nuevo para tu misma cuenta')
  expect(nodes(root).some(n => n.id === 'activation-pin')).toBe(false)
})
it('requires an explicit sign out before choosing a PIN for someone else', async () => {
  vi.mocked(post).mockResolvedValue({ name: 'María', username: 'maria' })
  const { root } = await mount(true)
  expect(text(root)).toContain('Primero, cierra la otra cuenta')
  expect(text(root)).toContain('Persona que ayuda')
  expect(nodes(root).some(n => n.id === 'activation-pin')).toBe(false)
  expect(post).toHaveBeenCalledTimes(1)
})
it('returns to an empty first step after server rejection, without consuming another attempt automatically', async () => {
  vi.mocked(post).mockResolvedValueOnce({ name: 'María', username: 'maria' }).mockRejectedValueOnce(new ApiError(410, 'enrollment_unavailable'))
  const { root } = await mount()
  fill(root, 'activation-pin', '642817'); await submit(root)
  fill(root, 'activation-confirm', '642817'); await submit(root)
  expect(text(root)).toContain('Pide uno nuevo para tu misma cuenta')
  expect(text(root)).toContain('Paso 1 de 2')
  expect(byId(root, 'activation-pin').value).toBe('')
  expect(post).toHaveBeenCalledTimes(2)
})

it('shows and hides PIN text without submitting or changing the PIN', async () => {
  vi.mocked(post).mockResolvedValue({ name: 'María', username: 'maria' })
  const { root } = await mount()
  fill(root, 'activation-pin', '642817'); await flush()
  expect(byId(root, 'activation-pin').type).toBe('password')
  const toggle = nodes(root).find(n => n.tagName === 'BUTTON' && text(n).includes('Mostrar PIN'))!
  toggle.click(); await flush()
  expect(byId(root, 'activation-pin').type).toBe('text')
  expect(byId(root, 'activation-pin').value).toBe('642817')
  expect(toggle.getAttribute('aria-pressed')).toBe('true')
  toggle.click(); await flush()
  expect(byId(root, 'activation-pin').type).toBe('password')
  expect(post).toHaveBeenCalledTimes(1)
})
