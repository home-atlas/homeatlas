// @vitest-environment happy-dom
import { afterEach, expect, it } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { modules } from '@/core/state'
import { setAuthState } from '@/core/auth'
import AppBreadcrumbs from './AppBreadcrumbs.vue'
const apps: ReturnType<typeof createApp>[] = []
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); modules.value = []; setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })
it('renders a semantic shared breadcrumb and returns to the filtered parent without cycling', async () => {
  setAuthState({ setupRequired: false, session: { user: { id: 'a', name: 'Ana', email: '' }, household: { id: 'h', name: 'Casa', timezone: 'UTC' }, role: 'owner', permissions: ['documents.read'], expiresAt: '' } })
  modules.value = [{ id: 'documents', name: 'Documents', version: '1', contractVersion: '1', enabled: true, available: true, accessPermission: 'documents.read', capabilities: [], dependencies: [], navigation: { path: '/documents', label: 'Documentos' } }]
  const router = createRouter({ history: createMemoryHistory(), routes: ['/', '/family', '/documents', '/documents/:id'].map(path => ({ path, component: { template: '<p />' } })) })
  await router.push('/family'); await router.push('/documents?q=manual&view=list'); await router.push('/documents/a')
  router.options.history.replace('/documents/a', { back: '/documents?q=manual&view=list' })
  const root = document.createElement('div'), app = createApp(AppBreadcrumbs).use(router)
  document.body.append(root); app.mount(root); apps.push(app); await nextTick()
  expect(root.querySelector('nav')?.getAttribute('data-slot')).toBe('breadcrumb')
  expect(root.querySelector('[aria-current=page]')?.textContent).toBe('Detalle')
  ;(root.querySelector('a[href="/documents"]') as HTMLAnchorElement).click()
  await new Promise(resolve => setTimeout(resolve, 0)); await nextTick()
  expect(router.currentRoute.value.fullPath).toBe('/documents?q=manual&view=list')
})
