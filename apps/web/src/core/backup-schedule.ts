import { checkResponse, download } from './api'

export interface BackupSchedule {
  enabled: boolean
  intervalHours: 24 | 168
  keep: number
  nextRunAt?: string
  passwordConfigured: boolean
}
export interface BackupHistoryEntry {
  id: string
  startedAt: string
  finishedAt?: string
  trigger: 'manual' | 'scheduled'
  status: 'running' | 'ready' | 'failed' | 'expired'
  errorCode?: string
  sizeBytes: number
  available: boolean
}
export interface BackupHistoryPage { items: BackupHistoryEntry[]; total: number }
async function scheduleRequest<T>(path: string, method = 'GET', body?: unknown, password = ''): Promise<T> {
  const response = await fetch(path, {
    method, credentials: 'same-origin', signal: AbortSignal.timeout(120000),
    headers: {
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      ...(method === 'PUT' ? { 'X-HomeAtlas-Confirm-Schedule': 'enable-and-retain' } : {}),
      ...(password ? { 'X-HomeAtlas-Backup-Password': btoa(String.fromCharCode(...new TextEncoder().encode(password))) } : {}),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  })
  await checkResponse(response)
  return response.json() as Promise<T>
}
export function getBackupSchedule() { return scheduleRequest<BackupSchedule>('/api/household/backups/schedule') }
export function saveBackupSchedule(schedule: Pick<BackupSchedule, 'enabled' | 'intervalHours' | 'keep'>, password: string) {
  return scheduleRequest<BackupSchedule>('/api/household/backups/schedule', 'PUT', schedule, schedule.enabled ? password : '')
}
export function runBackupSchedule() { return scheduleRequest<{ id: string }>('/api/household/backups/schedule/run', 'POST') }
export function getBackupHistory(offset = 0) { return scheduleRequest<BackupHistoryPage>(`/api/household/backups/history?offset=${offset}`) }
export function downloadHistoryBackup(id: string) { return download(`/api/household/backups/history/${encodeURIComponent(id)}/download`) }
export function backupHistoryError(code: string) {
  switch (code) {
    case 'permission_denied': return 'Se pausó la programación porque el propietario perdió sus permisos. Revisa los accesos y vuelve a configurarla.'
    case 'backup_original_unavailable': return 'Faltan originales o están dañados. Se conservaron las copias anteriores.'
    case 'backup_schedule_key_unavailable': return 'No se pudo abrir la contraseña guardada. Revisa la clave privada del servidor o configura de nuevo la contraseña.'
    case 'backup_retention_failed': return 'La copia es válida, pero no se pudieron retirar todas las copias antiguas. Se reintentará tras la próxima copia correcta.'
    case 'backup_interrupted': return 'La copia se interrumpió. Las copias anteriores se conservaron.'
    case 'backup_too_large': return 'La copia supera el límite de 1 GiB. Las copias anteriores se conservaron.'
    default: return 'No se pudo completar la copia. Revisa el almacenamiento y el estado del servidor.'
  }
}
export function backupHistoryStatus(entry: BackupHistoryEntry) {
  if (entry.status === 'ready') return entry.available ? 'Disponible' : 'Archivo no disponible'
  return { running: 'En curso', failed: 'Fallida', expired: 'Retirada por retención' }[entry.status]
}
