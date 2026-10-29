import { describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { canOpenModule, moduleForPath, registerModules, type ModuleStatus } from './modules'

function fixture(enabled: boolean): ModuleStatus {
  return {
    id: 'documents', name: 'Documents', version: '0.1.0', contractVersion: '1', enabled, available: true, accessPermission: 'documents.read',
    capabilities: ['documents.read'], dependencies: [],
    navigation: { label: 'Documentos', path: '/documents' },
  }
}

describe('built-in module registration', () => {
  it('removes and restores routes in the same router without duplicates', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [] })
    await registerModules(router, [fixture(true)])
    await registerModules(router, [fixture(true)])
    expect(router.getRoutes().filter(route => route.name === 'documents')).toHaveLength(1)
    await registerModules(router, [fixture(false)])
    expect(router.hasRoute('documents')).toBe(false)
    expect(router.hasRoute('document-detail')).toBe(false)
    await registerModules(router, [fixture(true)])
    expect(router.resolve('/documents/a').name).toBe('document-detail')
    await registerModules(router, [])
    expect(router.getRoutes()).toHaveLength(0)
  })

  it('does not restore a module from a superseded lazy load or stale account', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [] })
    const pending = registerModules(router, [fixture(true)])
    await registerModules(router, [])
    await pending
    expect(router.getRoutes()).toHaveLength(0)
    await registerModules(router, [fixture(true)], () => false)
    expect(router.getRoutes()).toHaveLength(0)
  })

  it('requires activation and explicit access without matching unrelated path prefixes', () => {
    expect(canOpenModule(fixture(true), ['documents.read'])).toBe(true)
    expect(canOpenModule(fixture(true), ['household.manage'])).toBe(false)
    expect(canOpenModule(fixture(false), ['documents.read'])).toBe(false)
    expect(canOpenModule({ ...fixture(true), available: false }, ['documents.read'])).toBe(false)
    expect(moduleForPath([fixture(true)], '/documents/a')?.id).toBe('documents')
    expect(moduleForPath([fixture(true)], '/documents-other')).toBeUndefined()
  })
  it('registers Documents only when the server enables it', async () => {
    for (const enabled of [false, true]) {
      const router = createRouter({ history: createMemoryHistory(), routes: [] })
      await registerModules(router, [fixture(enabled)])
      expect(router.hasRoute('documents')).toBe(enabled)
      if (enabled) expect(router.resolve('/documents').name).toBe('documents')
    }
  })

  it('rejects unknown modules and incompatible contracts', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [] })
    await expect(registerModules(router, [{ ...fixture(true), id: 'unknown' }])).rejects.toThrow('incompatible')
    await expect(registerModules(router, [{ ...fixture(true), contractVersion: '2' }])).rejects.toThrow('incompatible')
    expect(router.getRoutes()).toHaveLength(0)
  })

  it('registers an independently installed WASI plugin without a bundled frontend', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ name: 'settings', path: '/settings', component: { template: '<p>Settings</p>' } }] })
    const plugin = { ...fixture(true), id: 'ext-tools', runtime: 'wasi-preview1', navigation: { label: 'Tools', path: '/ext-tools' } }
    await registerModules(router, [plugin])
    expect(router.resolve('/ext-tools/energy').name).toBe('ext-tools-view')
    await registerModules(router, [])
    expect(router.hasRoute('ext-tools')).toBe(false)
    expect(router.hasRoute('settings')).toBe(true)
    await expect(registerModules(router, [{ ...plugin, navigation: { label: 'Tools', path: '/settings' } }])).rejects.toThrow('incompatible')
    expect(router.hasRoute('settings')).toBe(true)
  })
})
