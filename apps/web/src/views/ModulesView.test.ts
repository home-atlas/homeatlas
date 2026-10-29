// @vitest-environment happy-dom
import { afterEach, expect, it } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAuthState } from '@/core/auth'
import { modules } from '@/core/state'
import ModulesView from './ModulesView.vue'

const apps: { unmount: () => void }[] = []
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); modules.value = []; setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })

async function mount() {
  setAuthState({ setupRequired: false, session: { user: { id: 'owner', name: 'Owner', email: '' }, household: { id: 'home', name: 'Home', timezone: 'UTC' }, role: 'owner', permissions: ['household.manage', 'documents.read'], expiresAt: '2030-01-01T00:00:00Z' } })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/modules', component: ModulesView }, { path: '/', component: { template: '<p>Inicio</p>' } }, { path: '/documents', component: { template: '<p>Biblioteca</p>' } }] })
  await router.push('/modules')
  const root = document.createElement('div'), app = createApp(ModulesView).use(router)
  document.body.append(root); apps.push(app); app.mount(root); await nextTick()
  return root
}

it('explains the Core-only catalog without advertising an installed Documents module', async () => {
  modules.value = []
  const root = await mount()
  expect(root.textContent).toContain('Esta instalación funciona sin módulos adicionales.')
  expect(root.textContent).not.toContain('Documents')
  expect(root.querySelector('a[href="/"]')?.textContent).toContain('Volver a Inicio')
  expect(root.querySelector('button')).toBeNull()
})

it('keeps the installed Documents module and its existing actions', async () => {
  modules.value = [{ id: 'documents', name: 'Documents', version: '1', contractVersion: '1', enabled: true, available: true, accessPermission: 'documents.read', capabilities: ['documents.read'], dependencies: [], navigation: { label: 'Documentos', path: '/documents' } }]
  const root = await mount()
  expect(root.textContent).not.toContain('Esta instalación funciona sin módulos adicionales.')
  expect(root.querySelector('a[href="/documents"]')?.textContent).toContain('Abrir módulo')
  expect(root.querySelector('button')?.textContent).toBe('Desactivar')
})
