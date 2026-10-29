import { ApiError } from '@/core/api'
export interface DocumentMetadata { title: string; category: string; description: string; tags: string[] }
export interface Document extends DocumentMetadata {
  ownerId: string; ownerName: string; id: string; createdAt: string; filename: string; mediaType: string; size: number; sha256: string; hasFile: boolean
}
export const categories = [
  { value: 'manuales', label: 'Manuales' }, { value: 'recibos', label: 'Recibos' },
  { value: 'contratos', label: 'Contratos' }, { value: 'garantias', label: 'Garantías' }, { value: 'otros', label: 'Otros' },
]
export const categoryLabel = (value: string) => categories.find(c => c.value === value)?.label ?? value
export { formatSize } from '@/core/format-size'
export { formatHouseholdDate as formatDate } from '@/core/datetime'
export function validateFile(file: File): string {
  if (!/\.(pdf|png|jpe?g|txt)$/i.test(file.name)) return 'Elige un archivo PDF, PNG, JPEG o TXT.'
  if (!file.size) return 'El archivo está vacío.'
  if (file.size > 20 * 1048576) return 'El archivo supera el límite de 20 MiB.'
  return ''
}
export function errorMessage(error: unknown): string {
  if (!(error instanceof ApiError)) return 'No pudimos conectar con el servidor. Revisa la conexión e inténtalo de nuevo.'
  if (error.status === 401) return 'Tu sesión ha caducado. Vuelve a iniciar sesión.'
  if (error.status === 403) return 'No tienes permiso para realizar esta acción.'
  if (error.status === 404) return 'Este documento no está disponible o no tienes acceso.'
  if (error.status === 413) return 'El archivo supera el límite permitido: 20 MiB por archivo.'
  if (error.status === 415) return 'El formato o el contenido del archivo no es compatible. Usa PDF, PNG, JPEG o TXT.'
  if (error.status === 409) return 'El archivo original no está disponible. Consulta a quien administra el servidor.'
  if (error.status === 429) return 'Hay otras cargas en curso. Espera unos segundos e inténtalo de nuevo.'
  if (error.status === 400) return 'Revisa el archivo y los metadatos: título, categoría y hasta 10 etiquetas de 40 caracteres.'
  return 'No pudimos completar la solicitud. Inténtalo de nuevo.'
}
