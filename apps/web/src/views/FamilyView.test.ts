// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError, get, post, remove } from '@/core/api'
import { setAuthState } from '@/core/auth'
import type { Family } from '@/core/household'
import FamilyView from './FamilyView.vue'
vi.mock('@/core/api', async original => ({ ...await original<typeof import('@/core/api')>(), get: vi.fn(), post: vi.fn(), patch: vi.fn(), remove: vi.fn() }))
const apps: { unmount: () => void }[] = []
let activeRouter: ReturnType<typeof createRouter>
const fixture: Family = { canManage: true, roles: ['admin', 'member', 'restricted', 'guest'], capabilities: ['documents.read', 'documents.write', 'documents.share'], members: [{ id: 'owner', name: 'Ana', username: 'ana', email: 'ana@example.test', role: 'owner', permissions: ['household.manage', 'documents.read'] }, { id: 'member', name: 'María', username: 'maria', assisted: true, role: 'restricted', permissions: ['documents.read'] }] }
const invitation = { id: 'invite-1', email: 'persona@example.test', role: 'member', permissions: ['documents.read'], status: 'pending', expiresAt: '2026-10-24T12:00:00Z' }
function login(manager = true) { setAuthState({ setupRequired: false, smtpEnabled: false, session: { user: { id: manager ? 'owner' : 'member', name: manager ? 'Ana' : 'María', email: '' }, household: { id: 'home', name: 'Casa', timezone: 'UTC' }, role: manager ? 'owner' : 'restricted', permissions: manager ? ['household.manage', 'documents.read'] : ['documents.read'], expiresAt: '2026-10-22T00:00:00Z' } }) }
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/family', component: FamilyView }, { path: '/login', component: { template: '<p>Login</p>' } }] })
  activeRouter = router
  await router.push('/family')
  const root = document.createElement('div'), app = createApp(FamilyView).use(router)
  document.body.append(root); apps.push(app); app.mount(root); await flush()
  return root
}
function button(text: string, scope: ParentNode = document) { return [...scope.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === text)! }
function input(id: string, value: string) { const field = document.querySelector<HTMLInputElement>('#' + id)!; field.value = value; field.dispatchEvent(new Event('input', { bubbles: true })) }
async function submitInvite() { document.querySelector('[role=dialog] form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush() }
async function selectTab(name: string) { button(name).dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 })); await flush() }
beforeEach(() => { login(); vi.mocked(get).mockImplementation(async path => path === '/api/household/members' ? structuredClone(fixture) : [invitation]) })
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.resetAllMocks(); setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })
it('starts with the member list, keeps forms out of the page and supports names without accents', async () => {
  const root = await mount()
  expect(root.textContent).toContain('María')
  expect(document.querySelector('#invite-email')).toBeNull(); expect(document.querySelector('#assisted-name')).toBeNull()
  expect(document.querySelector('[role=dialog]')).toBeNull()
  expect(root.textContent).not.toContain(invitation.email)
  input('family-search', 'maria'); await flush()
  expect(root.textContent).toContain('María'); expect(root.textContent).not.toContain('@ana')
  input('family-search', 'nadie'); await flush()
  expect(root.textContent).toContain('No encontramos a esa persona')
  button('Ver a todos').click(); await flush(); expect(root.textContent).toContain('@ana')
})
it('creates invitations only inside a drawer and clears the one-time link when closed', async () => {
  vi.mocked(post).mockResolvedValue({ url: 'https://example.test/join#private-test-link', emailStatus: 'not_requested' })
  await mount(); button('Invitar a alguien').click(); await flush()
  expect(document.querySelector('#invite-email')).not.toBeNull()
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('Invitar no da acceso a todos los documentos')
  expect(document.querySelector('#invite-role-description')?.textContent).toContain('Usa los módulos con los permisos que elijas')
  expect(button('Crear invitación').form?.id).toBe('household-invitation-form')
  expect(button('Crear invitación').closest('form')).toBeNull()
  input('invite-email', 'persona@example.test'); await submitInvite()
  expect(post).toHaveBeenCalledWith('/api/household/invitations', { email: 'persona@example.test', role: 'member', permissions: ['documents.read'], sendEmail: false }, 30000)
  expect(document.querySelector<HTMLInputElement>('#invitation-link')?.value).toContain('private-test-link')
  expect(document.querySelector('#invite-email')).toBeNull()
  button('Terminar').click(); await flush()
  expect(document.querySelector('#invitation-link')).toBeNull()
  button('Invitar a alguien').click(); await flush()
  expect(document.querySelector<HTMLInputElement>('#invite-email')?.value).toBe('')
  expect(document.querySelector('#invitation-link')).toBeNull()
})
it('keeps the created link available if refreshing members temporarily fails', async () => {
  vi.mocked(post).mockResolvedValue({ url: 'https://example.test/join#keep-me', emailStatus: 'not_requested' })
  await mount(); button('Invitar a alguien').click(); await flush()
  vi.mocked(get).mockRejectedValueOnce(new ApiError(503))
  input('invite-email', 'persona@example.test'); await submitInvite()
  expect(document.querySelector<HTMLInputElement>('#invitation-link')?.value).toContain('keep-me')
})
it('keeps invitation errors in the drawer without losing the form', async () => {
  vi.mocked(post).mockRejectedValue(new ApiError(403))
  await mount(); button('Invitar a alguien').click(); await flush()
  input('invite-email', 'persona@example.test'); await submitInvite()
  expect(document.querySelector('[role=dialog] [role=alert]')?.textContent).toContain('No tienes permiso')
  expect(document.querySelector('#invite-email')).not.toBeNull()
})
it('hides invitation and assisted controls for members without management permissions', async () => {
  login(false); vi.mocked(get).mockResolvedValue({ ...fixture, canManage: false, roles: [], capabilities: [] })
  const root = await mount()
  expect(root.textContent).not.toContain('Invitar a alguien'); expect(root.textContent).not.toContain('Preparar acceso con ayuda')
  expect(root.querySelectorAll('[role=tab]')).toHaveLength(1)
  expect(get).toHaveBeenCalledTimes(1)
})
it('requires confirmation before revoking a pending invitation', async () => {
  await mount(); await selectTab('Invitaciones')
  expect(document.body.textContent).toContain(invitation.email)
  button('Revocar').click(); await flush()
  expect(remove).not.toHaveBeenCalled()
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('El enlace dejará de funcionar')
  button('Cancelar').click(); await flush(); expect(remove).not.toHaveBeenCalled()
})
it('discards private member responses after the session ends', async () => {
  let resolve!: (value: unknown) => void
  vi.mocked(get).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const root = await mount(); setAuthState({ setupRequired: false, session: null })
  resolve(fixture); await flush()
  expect(root.textContent).not.toContain('María'); expect(root.textContent).not.toContain('Invitar a alguien')
})
it('protects the owner and restores focus after viewing a member from its menu', async () => {
  const root = await mount()
  const trigger = root.querySelector<HTMLButtonElement>('#member-actions-owner')!
  trigger.click(); await flush()
  expect(document.querySelector('[role=menu]')?.textContent).toContain('Ver permisos')
  expect(document.querySelector('[role=menu]')?.textContent).not.toContain('Editar rol')
  expect(document.querySelector('[role=menu]')?.textContent).not.toContain('Retirar')
  const view = [...document.querySelectorAll<HTMLElement>('[role=menuitem]')].find(item => item.textContent?.includes('Ver permisos'))!
  view.click(); await flush()
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('El propietario está protegido')
  expect(document.querySelector('#edit-member-role')).toBeNull()
  button('Cerrar', document.querySelector('[role=dialog]')!).click(); await flush(); await flush()
  expect(document.activeElement).toBe(trigger)
})
it('removes management actions immediately when the session loses its permissions', async () => {
  const root = await mount(); button('Invitar a alguien').click(); await flush()
  expect(document.querySelector('#invite-email')).not.toBeNull()
  vi.mocked(get).mockResolvedValue({ ...fixture, canManage: false, roles: [], capabilities: [] })
  login(false); await flush()
  expect(document.querySelector('[role=dialog]')).toBeNull()
  expect(root.textContent).not.toContain('Invitar a alguien')
  expect(root.textContent).not.toContain('Preparar acceso con ayuda')
})

it('restores the invitations tab through back and forward without fetching again', async () => {
  await mount()
  const calls = vi.mocked(get).mock.calls.length
  await selectTab('Invitaciones')
  expect(activeRouter.currentRoute.value.query.tab).toBe('invitations')
  activeRouter.back(); await flush()
  expect(document.querySelector('[role=tab][aria-selected=true]')?.textContent).toContain('Miembros')
  activeRouter.forward(); await flush()
  expect(document.querySelector('[role=tab][aria-selected=true]')?.textContent).toContain('Invitaciones')
  expect(get).toHaveBeenCalledTimes(calls)
})
