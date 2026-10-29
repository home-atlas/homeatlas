import { computed, shallowRef } from 'vue'
import type { Router } from 'vue-router'
import { canOpenModule, registerModules, type ModuleStatus } from './modules'
import { get } from './api'
import { session } from './auth'

export const modules = shallowRef<ModuleStatus[]>([])
export const connectionError = shallowRef('')
export const healthy = shallowRef(false)
export const navigationModules = computed(() => modules.value.filter(module => canOpenModule(module, session.value?.permissions ?? [])))
const moduleRevisions = new WeakMap<Router, number>()
const pendingModules = new WeakMap<Router, { context: string; promise: Promise<boolean> }>()
const moduleContext = () => JSON.stringify([session.value?.user.id, session.value?.household.id, session.value?.role, session.value?.permissions])

export function refreshModules(router: Router, force = false): Promise<boolean> {
  const context = moduleContext(), pending = pendingModules.get(router)
  // Navigation and focus checks must not cancel each other for the same account.
  if (!force && pending?.context === context) return pending.promise
  const revision = (moduleRevisions.get(router) ?? 0) + 1
  moduleRevisions.set(router, revision)
  const account = session.value
  const current = () => revision === moduleRevisions.get(router) && context === moduleContext()
  const promise = (async () => {
    const installed = account ? await get<ModuleStatus[]>('/api/modules') : []
    if (!current()) return false
    const applied = await registerModules(router, installed, current)
    if (applied && current()) { modules.value = installed; return true }
    return false
  })()
  const task = { context, promise }
  pendingModules.set(router, task)
  const clear = () => { if (pendingModules.get(router) === task) pendingModules.delete(router) }
  void promise.then(clear, clear)
  return promise
}
