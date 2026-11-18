// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { get, patch, post, remove } from '@/core/api'
import { setAuthState, type AuthState } from '@/core/auth'
import AccountView from './AccountView.vue'
vi.mock('@/core/api', async original => ({ ...await original<typeof import('@/core/api')>(), get: vi.fn(), patch: vi.fn(), post: vi.fn(), remove: vi.fn() }))
const apps: { unmount(): void }[] = []
let state: AuthState
const profile = () => ({ user: state.session!.user, hasPassword: true })
const sessions = { sessions: [{ id: 'current-handle', current: true, method: 'password', lastSeenAt: '2026-11-18T10:00:00Z', expiresAt: '2026-11-18T20:00:00Z' }, { id: 'another-handle', current: false, method: 'pin', lastSeenAt: '2026-11-18T09:00:00Z', expiresAt: '2026-11-18T20:00:00Z' }], more: false }
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/settings/account', component: AccountView }, { path: '/login', component: { template: '<p>Entrar</p>' } }] })
  await router.push('/settings/account')
  const root = document.createElement('div'), app = createApp(AccountView).use(router); apps.push(app); document.body.append(root); app.mount(root); await flush()
  return { root, router }
}
beforeEach(() => {
  state = { setupRequired: false, session: { id: 'session-one', user: { id: 'alice', name: 'Alicia', email: 'alice@example.test', username: 'alice' }, household: { id: 'home', name: 'Casa', timezone: 'UTC' }, role: 'member', permissions: [], expiresAt: '2026-11-18T20:00:00Z' } }
  setAuthState(state)
  vi.mocked(get).mockImplementation(async path => {
    if (path === '/api/auth/profile') return profile()
    if (path === '/api/auth/sessions') return sessions
    if (path === '/api/auth/access') return { username: 'alice', hasPassword: true, canUsePIN: false, devices: [] }
    if (path === '/api/auth/session') return state
    throw new Error(path)
  })
  vi.mocked(remove).mockResolvedValue(); vi.mocked(post).mockResolvedValue(undefined)
})
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.resetAllMocks(); setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })
it('edits only the visible name and refreshes the session after saving', async () => {
  vi.mocked(patch).mockImplementation(async (_path, body) => { state = { ...state, session: { ...state.session!, user: { ...state.session!.user, name: (body as { name: string }).name } } }; return profile() })
  const { root } = await mount()
  const field = root.querySelector<HTMLInputElement>('#profile-name')!; field.value = 'Alicia nueva'; field.dispatchEvent(new Event('input', { bubbles: true })); await nextTick()
  field.closest('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
  expect(patch).toHaveBeenCalledWith('/api/auth/profile', { name: 'Alicia nueva' })
  expect(root.textContent).toContain('Tu nombre se ha guardado.'); expect(get).toHaveBeenCalledWith('/api/auth/session')
})
it('requires confirmation before revoking another session', async () => {
  const { root } = await mount()
  const button = Array.from(root.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Cerrar sesión')!
  button.click(); await flush(); expect(remove).not.toHaveBeenCalled()
  const dialog = document.querySelector('[role=dialog]')!; expect(dialog.textContent).toContain('Tu sesión actual seguirá abierta')
  Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Confirmar cierre'))!.click(); await flush()
  expect(remove).toHaveBeenCalledWith('/api/auth/sessions/another-handle')
})
it('confirms bulk revocation without sending an account or household chosen by the browser', async () => {
  const { root } = await mount()
  Array.from(root.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Cerrar las demás sesiones')!.click(); await flush()
  expect(post).not.toHaveBeenCalled()
  Array.from(document.querySelector('[role=dialog]')!.querySelectorAll('button')).find(button => button.textContent?.includes('Confirmar cierre'))!.click(); await flush()
  expect(post).toHaveBeenCalledWith('/api/auth/sessions/revoke-others')
})
it('discards profile/session responses after the account changes', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(get).mockImplementation(async path => {
    if (path === '/api/auth/profile' && state.session!.user.id === 'alice') return new Promise(done => { resolve = done })
    if (path === '/api/auth/profile') return profile()
    if (path === '/api/auth/sessions') return { sessions: [], more: false }
    if (path === '/api/auth/access') return { username: 'bob', hasPassword: false, canUsePIN: true, devices: [] }
    throw new Error(path)
  })
  const { root } = await mount()
  state = { ...state, session: { ...state.session!, id: 'session-bob', user: { id: 'bob', name: 'Roberto', email: '', username: 'bob' } } }; setAuthState(state); await flush()
  resolve({ user: { id: 'alice', name: 'Perfil privado anterior', email: 'private@example.test' }, hasPassword: true }); await flush()
  expect(root.querySelector<HTMLInputElement>('#profile-name')?.value).toBe('Roberto')
  expect(root.textContent).not.toContain('Perfil privado anterior'); expect(root.textContent).not.toContain('private@example.test')
})
