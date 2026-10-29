import { createApp } from 'vue'
import './style.css'
import App from './App.vue'
import { installNavigationFocus } from './core/navigation'
import { router } from './router'
import { get } from './core/api'
import { connectionError, healthy, modules, refreshModules } from './core/state'
import { canOpenModule, moduleForPath } from './core/modules'
import { authRedirect, refreshSession } from './core/auth'

async function start() {
  try {
    const health = await get<{ status: string }>('/api/health')
    if (health.status !== 'ok') throw new Error('Servidor no disponible.')
    healthy.value = true
  } catch {
    connectionError.value = 'Comprueba que el servidor esté disponible y vuelve a intentarlo.'
  }
  const app = createApp(App)
  installNavigationFocus(router)
  app.use(router)
  await router.isReady()
  app.mount('#app')
  let checking = false
  async function recheck() {
    if (checking || document.visibilityState === 'hidden') return
    checking = true
    try {
      const state = await refreshSession()
      if (!await refreshModules(router)) return
      healthy.value = true
      connectionError.value = ''
      const redirect = authRedirect(router.currentRoute.value, state)
      if (redirect) await router.replace(redirect)
      else {
        const current = router.currentRoute.value
        const module = moduleForPath(modules.value, current.path)
        if (module && !canOpenModule(module, state.session?.permissions ?? [])) await router.replace('/modules')
        else if (router.resolve(current.fullPath).name !== current.name) await router.replace(current.fullPath)
      }
    } catch {
      healthy.value = false
      connectionError.value = 'No pudimos comprobar tu sesión. Revisa la conexión e inténtalo de nuevo.'
    } finally { checking = false }
  }
  window.addEventListener('focus', () => { void recheck() })
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') void recheck() })
  window.addEventListener('pageshow', event => { if (event.persisted) void recheck() })
}
void start()
