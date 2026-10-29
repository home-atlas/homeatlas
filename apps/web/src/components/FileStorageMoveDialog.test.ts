// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import FileStorageMoveDialog from './FileStorageMoveDialog.vue'
import { ApiError, get, post } from '@/core/api'
vi.mock('@/core/api', async original => ({ ...await original<typeof import('@/core/api')>(), get: vi.fn(), post: vi.fn() }))
const initial = { target: 's3', revision: 'reviewed', local: 2, remote: 0, localBytes: 40, remoteBytes: 0, pendingCleanup: 0 }
let unmount: (() => void) | undefined
function mount() { const root = document.createElement('div'); document.body.append(root); const app = createApp(FileStorageMoveDialog); app.mount(root); unmount = () => app.unmount() }
function button(text: string) { return [...document.querySelectorAll<HTMLButtonElement>('button')].find(x => x.textContent?.trim() === text)! }
async function confirm() { document.getElementById('storage-move-confirm')!.click(); await nextTick() }
afterEach(() => { unmount?.(); unmount = undefined; document.body.innerHTML = ''; vi.resetAllMocks() })
it('requires a reviewed confirmation and moves one file per request with progress', async () => {
  vi.mocked(get).mockResolvedValue(initial)
  vi.mocked(post).mockResolvedValueOnce({ ...initial, moved: true, local: 1, remote: 1, localBytes: 20, remoteBytes: 20 }).mockResolvedValueOnce({ ...initial, moved: true, local: 0, remote: 2, localBytes: 0, remoteBytes: 40 })
  mount(); await vi.waitFor(() => expect(button('Mover a S3')).toBeDefined())
  expect(button('Mover a S3').disabled).toBe(true); expect(post).not.toHaveBeenCalled()
  await confirm(); button('Mover a S3').click()
  await vi.waitFor(() => expect(document.body.textContent).toContain('Traslado terminado'))
  expect(post).toHaveBeenCalledTimes(2)
  expect(post).toHaveBeenCalledWith('/api/household/storage/move', { revision: 'reviewed', confirm: 'move-and-remove-source' }, 120000)
  expect(document.body.textContent).toContain('2 · 0 pendientes')
})
it('stops after the active file and can resume with the remaining original', async () => {
  vi.mocked(get).mockResolvedValue(initial)
  let resolve: (value: unknown) => void = () => {}
  vi.mocked(post).mockImplementationOnce(() => new Promise(r => { resolve = r }))
  mount(); await vi.waitFor(() => expect(button('Mover a S3')).toBeDefined()); await confirm(); button('Mover a S3').click(); await nextTick()
  button('Detener después de este archivo').click(); await nextTick()
  resolve({ ...initial, moved: true, local: 1, remote: 1, localBytes: 20, remoteBytes: 20 })
  await vi.waitFor(() => expect(document.body.textContent).toContain('Traslado detenido'))
  expect(post).toHaveBeenCalledTimes(1)
  vi.mocked(post).mockResolvedValueOnce({ ...initial, moved: true, local: 0, remote: 2 })
  await confirm(); button('Mover a S3').click()
  await vi.waitFor(() => expect(document.body.textContent).toContain('Traslado terminado'))
  expect(post).toHaveBeenCalledTimes(2)
})
it('reports pending source cleanup and stops automatically before moving another file', async () => {
  vi.mocked(get).mockResolvedValue(initial)
  vi.mocked(post).mockResolvedValueOnce({ ...initial, moved: true, local: 1, remote: 1, pendingCleanup: 1 })
  mount(); await vi.waitFor(() => expect(button('Mover a S3')).toBeDefined()); await confirm(); button('Mover a S3').click()
  await vi.waitFor(() => expect(document.body.textContent).toContain('queda retirar el original anterior'))
  expect(post).toHaveBeenCalledTimes(1); expect(button('Reintentar limpieza').disabled).toBe(true)
})
it('preserves a partial transfer on failure and refreshes counts before a retry', async () => {
  vi.mocked(get).mockResolvedValueOnce(initial).mockResolvedValueOnce({ ...initial, local: 1, remote: 1 })
  vi.mocked(post).mockResolvedValueOnce({ ...initial, moved: true, local: 1, remote: 1 }).mockRejectedValueOnce(new ApiError(503, 'storage_unavailable'))
  mount(); await vi.waitFor(() => expect(button('Mover a S3')).toBeDefined()); await confirm(); button('Mover a S3').click()
  await vi.waitFor(() => expect(document.body.textContent).toContain('Los archivos ya trasladados siguen disponibles'))
  button('Actualizar revisión').click(); await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(button('Mover a S3').disabled).toBe(true)
})
