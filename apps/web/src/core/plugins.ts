import type { ModuleStatus, WebModule } from './modules'
import { collectionFrontend } from './collections'

export interface PluginField { name: string; label: string; type: 'text' | 'number' | 'textarea'; required: boolean }
export interface PluginView { id: string; title: string; description: string; submitLabel: string; fields: PluginField[]; readOnly?: boolean; permission?: 'share' }
export interface PluginManifest { id: string; name: string; version: string; contractVersion: string; views: PluginView[]; storage?: 'user' | 'shared' }

// Packages describe data, not executable browser code. One lazy renderer serves
// every installed WASI plugin without adding routes to the Core source.
export function pluginFrontend(module: ModuleStatus): WebModule {
  if (!/^[a-z][a-z0-9-]{0,47}$/.test(module.id) || module.navigation.path !== '/' + module.id) throw new Error('Ruta de plugin incompatible.')
  if(module.collection) return collectionFrontend(module)
  return { id: module.id, routes: [
    { name: module.id, path: '/' + module.id, component: () => import('@/views/PluginView.vue'), props: { pluginId: module.id } },
    { name: module.id + '-view', path: '/' + module.id + '/:view', component: () => import('@/views/PluginView.vue'), props: route => ({ pluginId: module.id, viewId: route.params.view }) },
  ] }
}
