import { get } from '@/core/api'
import type { HomeContribution } from '@/core/home'
import { categoryLabel, formatDate, type Document } from './model'
import image from '@/assets/home-documents.jpg'

export const home: HomeContribution = {
  caption: 'Tus documentos', recentCaption: 'Último documento', recentTitle: 'Añadidos recientemente',
  loadingText: 'Cargando documentos…', icon: 'document', image, tone: 'amber',
  collection: { label: 'Ver documentos', to: '/documents' },
  empty: { title: 'Todo en su lugar', description: 'Manuales, contratos y archivos de casa.', to: '/documents' },
  space: { description: 'Contratos, manuales y mucho más.' },
  suggestions: [
    { label: 'Un manual de casa', term: 'manual', icon: 'book', tone: 'blue' },
    { label: 'Un recibo de casa', term: 'recibo', icon: 'document', tone: 'amber' },
    { label: 'Un contrato importante', term: 'contrato', icon: 'folder', tone: 'warm' },
  ],
  async load() {
    const documents = await get<Document[]>('/api/documents')
    return documents.slice(0, 3).map(document => ({ id: document.id, title: document.title, description: categoryLabel(document.category) + ' · ' + formatDate(document.createdAt), to: '/documents/' + document.id, date: formatDate(document.createdAt) }))
  },
}
