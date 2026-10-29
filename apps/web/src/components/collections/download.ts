import { download } from '@/core/api'
import type { Item } from './model'

export async function downloadOriginal(collectionId: string, record: Item, current: () => boolean) {
  const blob = await download('/api/' + collectionId + '/' + encodeURIComponent(record.id) + '/download')
  if (!current()) return
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url; link.download = record.filename
  document.body.appendChild(link); link.click(); link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 60000)
}
