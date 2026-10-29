import { download } from '@/core/api'
import type { Document } from './model'

export async function downloadOriginal(record: Document, current: () => boolean) {
  const blob = await download('/api/documents/' + encodeURIComponent(record.id) + '/download')
  if (!current()) return
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url; link.download = record.filename
  document.body.appendChild(link); link.click(); link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 60000)
}
