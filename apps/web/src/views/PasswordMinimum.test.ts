// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { post } from '@/core/api'
import { setAuthState } from '@/core/auth'
import SetupView from './SetupView.vue'
import RecoveryView from './RecoveryView.vue'
import JoinView from './JoinView.vue'
vi.mock('@/core/api', async importOriginal => ({ ...await importOriginal<typeof import('@/core/api')>(), post: vi.fn() }))
const apps: { unmount: () => void }[] = []
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
function fill(root: HTMLElement, id: string, value: string) { const input = root.querySelector<HTMLInputElement>('#' + id)!; input.value = value; input.dispatchEvent(new Event('input', { bubbles: true })) }
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.resetAllMocks(); document.body.innerHTML = ''; setAuthState({ setupRequired: false, session: null }) })
it('toggles each setup password without submitting and keeps help collapsed', async () => {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/setup', component: SetupView }] })
  await router.push('/setup')
  const root = document.createElement('div'), app = createApp(SetupView).use(router)
  document.body.append(root); apps.push(app); app.mount(root); await flush()
  fill(root, 'setup-password', 'test-password'); fill(root, 'setup-confirm', 'test-password')
  const password = root.querySelector<HTMLInputElement>('#setup-password')!
  const confirmation = root.querySelector<HTMLInputElement>('#setup-confirm')!
  const toggle = root.querySelector<HTMLButtonElement>('[aria-controls="setup-password"]')!
  const confirmToggle = root.querySelector<HTMLButtonElement>('[aria-controls="setup-confirm"]')!
  expect(password.type).toBe('password'); expect(confirmation.type).toBe('password')
  expect(toggle.type).toBe('button'); expect(toggle.getAttribute('aria-label')).toBe('Mostrar contraseña')
  toggle.click(); await flush()
  expect(password.type).toBe('text'); expect(confirmation.type).toBe('password')
  expect(toggle.getAttribute('aria-label')).toBe('Ocultar contraseña')
  expect(toggle.getAttribute('aria-pressed')).toBe('true')
  confirmToggle.click(); await flush()
  expect(confirmation.type).toBe('text')
  expect(password.value).toBe('test-password'); expect(confirmation.value).toBe('test-password')
  expect(post).not.toHaveBeenCalled()
  toggle.click(); await flush()
  expect(password.type).toBe('password'); expect(toggle.getAttribute('aria-pressed')).toBe('false')
  expect(root.textContent).not.toContain('está en el archivo privado')
  const help = [...root.querySelectorAll('button')].find(button => button.textContent?.includes('Ayuda para el alta'))!
  expect(help.getAttribute('aria-expanded')).toBe('false')
  help.click(); await flush()
  expect(help.getAttribute('aria-expanded')).toBe('true')
  expect(root.textContent).toContain('está en el archivo privado')
  fill(root, 'setup-confirm', 'different-password')
  root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
  expect(password.type).toBe('password'); expect(confirmation.type).toBe('password')
  expect(root.querySelector('[role=alert]')?.textContent).toContain('no coinciden')
  expect(post).not.toHaveBeenCalled()
})
for (const scenario of [
  { label: 'setup', component: SetupView, path: '/setup', password: 'setup-password', confirmation: 'setup-confirm', endpoint: '/api/setup' },
  { label: 'recovery', component: RecoveryView, path: '/forgot-password', password: 'recovery-password', confirmation: 'recovery-confirm', endpoint: '/api/auth/recover' },
  { label: 'invitation', component: JoinView, path: '/join', password: 'join-password', confirmation: 'join-confirmation', endpoint: '/api/invitations/accept' },
]) {
  it(`rejects seven characters locally and sends eight in ${scenario.label}`, async () => {
    setAuthState({ setupRequired: scenario.label === 'setup', session: null })
    const submitted: { path: string; password: string }[] = []
    vi.mocked(post).mockImplementation(async (path, body) => {
      if (path === '/api/invitations/inspect') return { email: 'member@example.test', householdName: 'Hogar', role: 'restricted', permissions: [], expiresAt: '2026-11-01T00:00:00Z', needsAccount: true } as never
      submitted.push({ path, password: (body as { password: string }).password })
      return { setupRequired: false, session: null } as never
    })
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: scenario.path, component: scenario.component }, ...['/', '/family', '/login'].map(path => ({ path, component: { template: '<p>Destino</p>' } }))] })
    await router.push(scenario.path + (scenario.label === 'invitation' ? '#test-only-token' : ''))
    const root = document.createElement('div'), app = createApp(scenario.component).use(router)
    document.body.append(root); apps.push(app); app.mount(root); await flush()
    if (scenario.label === 'setup') { fill(root, 'setup-code', 'test-only-code'); fill(root, 'owner-name', 'Owner'); fill(root, 'setup-username', 'owner'); fill(root, 'household-name', 'Hogar') }
    if (scenario.label === 'recovery') fill(root, 'recovery-code', 'test-only-code')
    if (scenario.label === 'invitation') fill(root, 'join-name', 'Member')
    fill(root, scenario.password, 'pass123'); fill(root, scenario.confirmation, 'pass123')
    expect(root.querySelector<HTMLInputElement>('#' + scenario.password)!.minLength).toBe(8)
    root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
    expect(submitted).toEqual([])
    expect(root.querySelector('[role=alert]')?.textContent).toContain('entre 8 y 128')
    fill(root, scenario.password, 'pass1234'); fill(root, scenario.confirmation, 'pass1234')
    root.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
    expect(submitted).toEqual([{ path: scenario.endpoint, password: 'pass1234' }])
  })
}
