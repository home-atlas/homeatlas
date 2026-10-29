// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError, get, post } from '@/core/api'
import { setAuthState } from '@/core/auth'
import LoginView from './LoginView.vue'
vi.mock('@/core/api', async importOriginal => ({ ...await importOriginal<typeof import('@/core/api')>(), get: vi.fn(), post: vi.fn() }))
const apps: { unmount: () => void }[] = []
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/login', component: LoginView }, { path: '/forgot-password', component: { template: '<p>Recuperar</p>' } }, { path: '/', component: { template: '<p>Hogar</p>' } }] })
  await router.push('/login')
  const root = document.createElement('div'), app = createApp(LoginView).use(router)
  document.body.append(root); apps.push(app); app.mount(root); await flush()
  return { root, router }
}
function input(root: HTMLElement, id: string, value: string) { const field = root.querySelector<HTMLInputElement>('#' + id)!; field.value = value; field.dispatchEvent(new Event('input', { bubbles: true })) }
async function submit(root: HTMLElement) { root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush() }
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.resetAllMocks(); setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })
it('starts with the private bound profile and enters using only its PIN', async () => {
  vi.mocked(get).mockResolvedValue({ available: true, name: 'María' })
  vi.mocked(post).mockResolvedValue({ setupRequired: false, session: null })
  const { root, router } = await mount()
  expect(root.textContent).toContain('Hola, María')
  expect(root.querySelector('#login-email')).toBeNull()
  input(root, 'login-pin', '642817'); await submit(root)
  expect(post).toHaveBeenCalledWith('/api/auth/pin/login', { pin: '642817' })
  expect(router.currentRoute.value.path).toBe('/')
  expect(root.querySelector<HTMLInputElement>('#login-pin')!.value).toBe('')
})
it('offers help after an incorrect PIN, clears it, and does not automatically retry', async () => {
  vi.mocked(get).mockResolvedValue({ available: true, name: 'María' })
  vi.mocked(post).mockRejectedValue(new ApiError(401))
  const { root } = await mount()
  input(root, 'login-pin', '111111'); await submit(root)
  expect(root.querySelector('[role=alert]')?.textContent).toContain('pide ayuda antes de volver a intentarlo')
  expect(root.textContent).toContain('¿Olvidaste tu PIN o necesitas ayuda?')
  expect(root.querySelector<HTMLInputElement>('#login-pin')!.value).toBe('')
  expect(document.activeElement?.id).toBe('login-pin')
  expect(post).toHaveBeenCalledTimes(1)
})
it('explains unavailable binding after rejection and keeps password login available', async () => {
  vi.mocked(get).mockResolvedValueOnce({ available: true, name: 'María' }).mockResolvedValueOnce({ available: false })
  vi.mocked(post).mockRejectedValue(new ApiError(401))
  const { root } = await mount()
  input(root, 'login-pin', '111111'); await submit(root)
  expect(root.querySelector('#login-pin')).toBeNull()
  expect(root.querySelector('[role=alert]')?.textContent).toContain('ya no está disponible')
  const password = [...root.querySelectorAll('button')].find(b => b.textContent?.includes('Volver al inicio de sesión'))!
  password.click(); await flush()
  expect(root.querySelector('#login-password')).not.toBeNull()
  expect(root.querySelector('[role=alert]')).toBeNull()
})
it('rejects incomplete PIN locally without spending an attempt on the server', async () => {
  vi.mocked(get).mockResolvedValue({ available: true, name: 'María' })
  const { root } = await mount()
  input(root, 'login-pin', '123'); await submit(root)
  expect(root.querySelector('[role=alert]')?.textContent).toContain('PIN completo')
  expect(post).not.toHaveBeenCalled()
})

it('puts the return action before the PIN title and returns without submitting credentials', async () => {
  vi.mocked(get).mockResolvedValue({ available: true, name: 'María' })
  const { root, router } = await mount()
  const back = [...root.querySelectorAll('button')].find(button => button.textContent?.includes('Volver al inicio de sesión'))!
  expect(back.compareDocumentPosition(root.querySelector('#login-title')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  input(root, 'login-pin', '123456'); back.click(); await flush()
  expect(root.querySelector('#login-pin')).toBeNull()
  expect(root.querySelector('#login-password')).not.toBeNull()
  expect(document.activeElement?.id).toBe('login-email')
  expect(router.currentRoute.value.path).toBe('/login')
  expect(post).not.toHaveBeenCalled()
  const pin = [...root.querySelectorAll('button')].find(button => button.textContent?.includes('Entrar con PIN'))!
  pin.click(); await flush()
  expect(root.querySelector<HTMLInputElement>('#login-pin')!.value).toBe('')
})
it('also offers return when this browser has no PIN binding', async () => {
  vi.mocked(get).mockResolvedValue({ available: false })
  const { root } = await mount()
  const pin = [...root.querySelectorAll('button')].find(button => button.textContent?.includes('Entrar con PIN'))!
  pin.click(); await flush()
  expect(root.textContent).toContain('Primero, prepara este equipo')
  const back = [...root.querySelectorAll('button')].find(button => button.textContent?.includes('Volver al inicio de sesión'))!
  back.click(); await flush()
  expect(root.querySelector('#login-email')).not.toBeNull()
  expect(root.textContent).not.toContain('Primero, prepara este equipo')
})
it('does not switch back to PIN when a delayed device lookup follows an explicit return', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(get).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const { root } = await mount()
  const pin = [...root.querySelectorAll('button')].find(button => button.textContent?.includes('Entrar con PIN'))!
  pin.click(); await flush()
  const back = [...root.querySelectorAll('button')].find(button => button.textContent?.includes('Volver al inicio de sesión'))!
  back.click(); await flush()
  resolve({ available: true, name: 'María' }); await flush()
  expect(root.querySelector('#login-email')).not.toBeNull()
  expect(root.querySelector('#login-pin')).toBeNull()
})

it('uses browser back and forward to restore password and PIN screens without credentials', async () => {
  vi.mocked(get).mockResolvedValue({ available: false })
  const { root, router } = await mount()
  input(root, 'login-password', 'example-secret')
  const pin = [...root.querySelectorAll('button')].find(button => button.textContent?.includes('Entrar con PIN'))!
  pin.click(); await flush()
  expect(router.currentRoute.value.query.method).toBe('pin')
  expect(root.textContent).toContain('Primero, prepara este equipo')
  router.back(); await flush()
  expect(root.querySelector('#login-password')).not.toBeNull()
  expect((root.querySelector('#login-password') as HTMLInputElement).value).toBe('')
  router.forward(); await flush()
  expect(root.textContent).toContain('Primero, prepara este equipo')
})

it('does not rewrite a recovery route when a late PIN device lookup resolves', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(get).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const { router } = await mount()
  await router.push('/forgot-password')
  resolve({ available: true, name: 'María' }); await flush()
  expect(router.currentRoute.value.fullPath).toBe('/forgot-password')
})
