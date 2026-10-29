import type { RouteRecordRaw, Router } from 'vue-router'
import type { HomeContribution } from './home'
import type { CollectionDefinition } from '@/components/collections/model'

export interface ModuleStatus {
  collection?: CollectionDefinition
  id: string
  name: string
  version: string
  contractVersion: string
  runtime?: string
  enabled: boolean
  available: boolean
  accessPermission: string
  navigation: { label: string; path: string }
  capabilities: string[]
  dependencies: string[]
}

export interface WebModule {
  home?: HomeContribution
  id: string
  routes: RouteRecordRaw[]
}

const frontends = new WeakMap<Router, Map<string, WebModule>>()
export function getWebModule(router: Router, id: string) { return frontends.get(router)?.get(id) }

const loaders: Record<string, () => Promise<{ default: WebModule }>> = import.meta.env.MODE === 'test' ? { '../modules/documents/index.ts': () => import('../test/legacy-documents/index') } : {}
const registrations = new WeakMap<Router, Map<string, (() => void)[]>>()
const revisions = new WeakMap<Router, number>()

export function canOpenModule(module: ModuleStatus, permissions: string[]) {
  return module.enabled && module.available && permissions.includes(module.accessPermission)
}

export function moduleForPath(modules: ModuleStatus[], path: string) {
  return modules.find(module => path === module.navigation.path || path.startsWith(module.navigation.path + '/'))
}

export async function registerModules(router: Router, modules: ModuleStatus[], current = () => true) {
  const revision = (revisions.get(router) ?? 0) + 1
  revisions.set(router, revision)
  const registered = registrations.get(router) ?? new Map<string, (() => void)[]>()
  const desired = new Map<string, WebModule>()
  for (const module of modules.filter(module => module.enabled && module.available)) {
    const load = module.collection ? undefined : loaders[`../modules/${module.id}/index.ts`]
    if (module.contractVersion !== '1' || (!load && module.runtime !== 'wasi-preview1')) {
      throw new Error(`Módulo incompatible: ${module.id}`)
    }
    const frontend = load ? (await load()).default : (await import('./plugins')).pluginFrontend(module)
    if (frontend.id !== module.id) throw new Error('La identidad del módulo no coincide.')
    desired.set(module.id, frontend)
  }
  if (revisions.get(router) !== revision || !current()) return false
  for (const [id, remove] of registered) {
    if (!desired.has(id)) { remove.forEach(fn => fn()); registered.delete(id) }
  }
  for (const [id, frontend] of desired) {
    if (!registered.has(id)) registered.set(id, frontend.routes.map(route => router.addRoute(route)))
  }
  registrations.set(router, registered)
  frontends.set(router, desired)
  return true
}
