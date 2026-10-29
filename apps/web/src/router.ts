import { createRouter, createWebHistory, type RouterHistory } from 'vue-router'
import { authRedirect, refreshSession } from './core/auth'
import { connectionError, healthy, modules, refreshModules } from './core/state'
import { scrollBehavior } from './core/navigation'
import { canOpenModule, moduleForPath } from './core/modules'

export function createAppRouter(history: RouterHistory) {
  const router = createRouter({
    history,
    scrollBehavior,
    routes: [
      { path: '/setup', name: 'setup', component: () => import('./views/SetupView.vue') },
      { path: '/join', name: 'join', component: () => import('./views/JoinView.vue') },
      { path: '/family', name: 'family', meta: { breadcrumb: 'Mi familia' }, component: () => import('./views/FamilyView.vue') },
      { path: '/activate-access', name: 'activate-access', component: () => import('./views/ActivateAccessView.vue') },
      { path: '/login', name: 'login', component: () => import('./views/LoginView.vue') },
      { path: '/forgot-password', name: 'forgot-password', component: () => import('./views/RecoveryView.vue') },
      { path: '/recover', redirect: to => ({ path: '/forgot-password', query: to.query, hash: to.hash }) },
      { path: '/verify-email', name: 'verify-email', component: () => import('./views/VerifyEmailView.vue') },
      { path: '/', name: 'home', meta: { breadcrumb: 'Inicio' }, component: () => import('./views/HomeView.vue') },
      { path: '/modules', name: 'modules', meta: { breadcrumb: 'Mis módulos' }, component: () => import('./views/ModulesView.vue') },
      { path: '/settings', name: 'settings', meta: { breadcrumb: 'Ajustes' }, component: () => import('./views/SettingsView.vue') },
      { path: '/:pathMatch(.*)*', component: () => import('./views/NotFoundView.vue') },
    ],
  })

  router.beforeEach(async to => {
    try {
      const state = await refreshSession()
      const redirect = authRedirect(to, state)
      if (!await refreshModules(router)) return false
      healthy.value = true
      connectionError.value = ''
      if (redirect) return { ...router.resolve(redirect), replace: true }
      const module = moduleForPath(modules.value, to.path)
      if (module && !canOpenModule(module, state.session?.permissions ?? [])) return { path: '/modules', replace: true }
      if (router.resolve(to.fullPath).name !== to.name) return to.fullPath
    } catch {
      healthy.value = false
      connectionError.value = 'No pudimos comprobar tu sesión. Revisa la conexión e inténtalo de nuevo.'
    }
  })

  return router
}

export const router = createAppRouter(createWebHistory())
