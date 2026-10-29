import type { WebModule } from '@/core/modules'
import { home } from './home'

export default {
  id: 'documents',
  home,
  routes: [
    { name: 'documents', path: '/documents', component: () => import('./DocumentsView.vue') },
    { name: 'document-detail', path: '/documents/:id', component: () => import('./DocumentDetailView.vue') },
  ],
} satisfies WebModule
