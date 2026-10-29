import { afterEach, expect, it } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAuthState } from '@/core/auth'
import LoginView from './LoginView.vue'
import SetupView from './SetupView.vue'
import ActivateAccessView from './ActivateAccessView.vue'
async function render(component: typeof LoginView | typeof SetupView | typeof ActivateAccessView, path: string) {
  setAuthState({ setupRequired: path === '/setup', session: null })
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path, component }] })
  await router.push(path)
  return renderToString(createSSRApp(component).use(router))
}
afterEach(() => setAuthState({ setupRequired: false, session: null }))
it('keeps username/password login and exposes the PIN alternative', async () => {
  const html = await render(LoginView, '/login')
  expect(html).toContain('Usuario o correo electrónico')
  expect(html).toContain('autocomplete="current-password"')
  expect(html).toContain('Entrar con PIN')
  expect(html).not.toContain('passkey')
})
it('allows initial setup without email while requiring a local username', async () => {
  const html = await render(SetupView, '/setup')
  expect(html.match(/<input[^>]*id="setup-email"[^>]*>/)?.[0]).not.toContain('required')
  expect(html.match(/<input[^>]*id="setup-username"[^>]*>/)?.[0]).toContain('required')
})
it('requires a private enrollment code rather than exposing public account creation', async () => {
  const html = await render(ActivateAccessView, '/activate-access')
  expect(html).toContain('Tengo un código en lugar de un enlace')
  expect(html).toContain('Abre el enlace que te compartieron')
  expect(html).not.toContain('id="activation-pin"')
})
