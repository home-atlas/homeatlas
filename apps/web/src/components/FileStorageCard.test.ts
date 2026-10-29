// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import FileStorageCard from './FileStorageCard.vue'
import { ApiError, get, patch } from '@/core/api'
import { setAuthState, type Session } from '@/core/auth'
vi.mock('@/core/api', async original => ({ ...await original<typeof import('@/core/api')>(), get: vi.fn(), patch: vi.fn() }))
const member: Session = { user: { id: 'owner', name: 'Owner', email: '' }, household: { id: 'home', name: 'Home', timezone: 'UTC' }, role: 'owner', permissions: ['household.manage'], expiresAt: '2027-01-01' }
const local = { provider: 'local', revision: '', credentialsConfigured: false, s3: { endpoint: '', region: 'us-east-1', bucket: '', prefix: '', pathStyle: true } }
let unmount: (() => void) | undefined
function mount() { setAuthState({ setupRequired: false, session: member }); const root = document.createElement('div'); document.body.append(root); const app = createApp(FileStorageCard); app.mount(root); unmount = () => app.unmount(); return root }
async function flush() { await nextTick(); await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
function button(text: string) { return [...document.querySelectorAll<HTMLButtonElement>('button')].find(x => x.textContent?.trim() === text)! }
afterEach(() => { unmount?.(); unmount = undefined; document.body.innerHTML = ''; vi.resetAllMocks(); setAuthState({ setupRequired: false, session: null }) })
it('defaults to local and offers S3 only in the configuration drawer', async () => {
  vi.mocked(get).mockResolvedValue(local); mount()
  await vi.waitFor(() => expect(document.body.textContent).toContain('Local · Este servidor'))
  expect(document.querySelector('[role="dialog"]')).toBeNull()
  button('Configurar almacenamiento').click(); await nextTick()
  expect(document.querySelector('#storage-provider')?.textContent).toContain('Local')
  expect(document.querySelector('#storage-secret-key')).toBeNull()
  document.querySelector<HTMLButtonElement>('#storage-provider')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); await flush()
  const option = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find(x => x.textContent?.includes('S3'))!
  expect(option).toBeDefined(); option.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); await flush()
  expect(document.querySelector<HTMLInputElement>('#storage-secret-key')?.required).toBe(true)
  expect(document.querySelector<HTMLInputElement>('#storage-access-key')?.type).toBe('password')
  expect(button('Probar y guardar S3')).toBeDefined()
  button('Cancelar').click(); await nextTick()
  expect(document.querySelector('#storage-secret-key')).toBeNull()
  expect(patch).not.toHaveBeenCalled()
})
it('keeps the form on failure and clears credentials after a successful save', async () => {
  vi.mocked(get).mockResolvedValue(local); mount(); await vi.waitFor(() => expect(button('Configurar almacenamiento')).toBeDefined())
  button('Configurar almacenamiento').click(); await nextTick(); document.querySelector<HTMLButtonElement>('#storage-provider')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); await flush()
  ;[...document.querySelectorAll<HTMLElement>('[role="option"]')].find(x => x.textContent?.includes('S3'))!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); await flush()
  for (const [id, value] of Object.entries({ 'storage-bucket': 'private-test', 'storage-access-key': 'fixture-access', 'storage-secret-key': 'fixture-secret' })) {
    const input = document.getElementById(id) as HTMLInputElement; input.value = value; input.dispatchEvent(new Event('input', { bubbles: true }))
  }
  vi.mocked(patch).mockRejectedValueOnce(new ApiError(503, 'storage_unavailable'))
  document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  await vi.waitFor(() => expect(document.body.textContent).toContain('comprueba la conexión'))
  expect(document.querySelector('#storage-secret-key')).not.toBeNull()
  expect(button('Probar y guardar S3').disabled).toBe(false)
  vi.mocked(patch).mockResolvedValueOnce({ ...local, provider: 's3', revision: 'saved', credentialsConfigured: true, s3: { ...local.s3, bucket: 'private-test' } })
  document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  await vi.waitFor(() => expect(document.body.textContent).toContain('Conexión comprobada'))
  expect(document.querySelector('#storage-secret-key')).toBeNull()
  expect(patch).toHaveBeenLastCalledWith('/api/household/storage', expect.objectContaining({ provider: 's3', s3: expect.objectContaining({ accessKey: 'fixture-access', secretKey: 'fixture-secret' }) }), 45000)
})
it('hides storage settings from administrators and discards late account responses', async () => {
  let resolve: (value: unknown) => void = () => {}
  vi.mocked(get).mockImplementation(() => new Promise(r => { resolve = r }))
  mount(); setAuthState({ setupRequired: false, session: { ...member, role: 'admin' } }); await nextTick()
  resolve(local); await nextTick(); await nextTick()
  expect(document.body.textContent).not.toContain('Almacenamiento de archivos')
  expect(patch).not.toHaveBeenCalled()
})
