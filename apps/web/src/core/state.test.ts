import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAuthState } from './auth'
import { modules, refreshModules } from './state'
import type { ModuleStatus } from './modules'

const fixture = (enabled: boolean): ModuleStatus => ({ id: 'documents', name: 'Documents', version: '1', contractVersion: '1', enabled, available: true, accessPermission: 'documents.read', capabilities: ['documents.read'], dependencies: [], navigation: { label: 'Documentos', path: '/documents' } })
const router = () => createRouter({ history: createMemoryHistory(), routes: [] })
function login(household = 'home-a') {
  setAuthState({ setupRequired: false, session: { user: { id: 'alice', name: 'Alice', email: 'alice@example.test' }, household: { id: household, name: household, timezone: 'UTC' }, role: 'owner', permissions: ['documents.read'], expiresAt: '2026-10-22T00:00:00Z' } })
}
beforeEach(() => { setAuthState({ setupRequired: false, session: null }); modules.value = [] })
afterEach(() => { vi.unstubAllGlobals(); setAuthState({ setupRequired: false, session: null }) })

it('does not fetch a private module catalog before login', async () => {
  const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
  await refreshModules(router())
  expect(fetcher).not.toHaveBeenCalled()
  expect(modules.value).toEqual([])
})

it('discards a household catalog arriving after a switch to another household', async () => {
  const r = router(); login()
  let resolve!: (response: Response) => void
  vi.stubGlobal('fetch', vi.fn().mockImplementationOnce(() => new Promise<Response>(done => { resolve = done })).mockResolvedValueOnce(Response.json([fixture(false)])))
  const old = refreshModules(r)
  login('home-b')
  await refreshModules(r)
  resolve(Response.json([fixture(true)]))
  expect(await old).toBe(false)
  expect(modules.value[0]?.enabled).toBe(false)
  expect(r.hasRoute('documents')).toBe(false)
})

it('discards a catalog arriving after logout', async () => {
  const r = router(); login()
  let resolve!: (response: Response) => void
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise<Response>(done => { resolve = done })))
  const old = refreshModules(r)
  setAuthState({ setupRequired: false, session: null })
  await refreshModules(r)
  resolve(Response.json([fixture(true)]))
  expect(await old).toBe(false)
  expect(modules.value).toEqual([])
  expect(r.hasRoute('documents')).toBe(false)
})

it('shares overlapping checks for the same account without caching the next check', async () => {
  const r = router(); login()
  let resolve!: (response: Response) => void
  const fetcher = vi.fn().mockImplementationOnce(() => new Promise<Response>(done => { resolve = done })).mockResolvedValueOnce(Response.json([fixture(true)]))
  vi.stubGlobal('fetch', fetcher)
  const navigation = refreshModules(r), focus = refreshModules(r)
  expect(fetcher).toHaveBeenCalledTimes(1)
  resolve(Response.json([fixture(true)]))
  expect(await Promise.all([navigation, focus])).toEqual([true, true])
  await refreshModules(r)
  expect(fetcher).toHaveBeenCalledTimes(2)
})
it('forces a fresh catalog after module changes and discards the older result', async () => {
  const r = router(); login()
  let resolve!: (response: Response) => void
  vi.stubGlobal('fetch', vi.fn().mockImplementationOnce(() => new Promise<Response>(done => { resolve = done })).mockResolvedValueOnce(Response.json([fixture(false)])))
  const old = refreshModules(r)
  expect(await refreshModules(r, true)).toBe(true)
  resolve(Response.json([fixture(true)]))
  expect(await old).toBe(false)
  expect(modules.value[0]?.enabled).toBe(false)
  expect(r.hasRoute('documents')).toBe(false)
})
it('discards a response after permissions change even for the same user and household', async () => {
  const r = router(); login()
  let resolve!: (response: Response) => void
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise<Response>(done => { resolve = done })))
  const old = refreshModules(r)
  setAuthState({ setupRequired: false, session: { user: { id: 'alice', name: 'Alice', email: '' }, household: { id: 'home-a', name: 'Casa', timezone: 'UTC' }, role: 'restricted', permissions: [], expiresAt: '' } })
  resolve(Response.json([fixture(true)]))
  expect(await old).toBe(false)
  expect(r.hasRoute('documents')).toBe(false)
})
it('clears failed checks so a subsequent navigation can retry', async () => {
  const r = router(); login()
  const fetcher = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(Response.json([fixture(true)]))
  vi.stubGlobal('fetch', fetcher)
  const first = refreshModules(r), overlapping = refreshModules(r)
  const failed = await Promise.allSettled([first, overlapping])
  expect(failed.map(result => result.status)).toEqual(['rejected', 'rejected'])
  expect(await refreshModules(r)).toBe(true)
  expect(fetcher).toHaveBeenCalledTimes(2)
})
