import { afterEach, expect, it, vi } from 'vitest'
import { backupHistoryError, backupHistoryStatus, downloadHistoryBackup, getBackupHistory, getBackupSchedule, runBackupSchedule, saveBackupSchedule } from './backup-schedule'

afterEach(() => vi.unstubAllGlobals())
it('saves the schedule with explicit retention consent and an encoded secret outside JSON and URLs', async () => {
  const fetcher = vi.fn().mockImplementation(async () => Response.json({ enabled: true, intervalHours: 24, keep: 7, passwordConfigured: true }))
  vi.stubGlobal('fetch', fetcher)
  await saveBackupSchedule({ enabled: true, intervalHours: 24, keep: 7 }, 'Contraseña segura 2026')
  expect(fetcher.mock.calls[0]?.[0]).toBe('/api/household/backups/schedule')
  expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ method: 'PUT', credentials: 'same-origin', body: JSON.stringify({ enabled: true, intervalHours: 24, keep: 7 }), headers: { 'X-HomeAtlas-Confirm-Schedule': 'enable-and-retain', 'X-HomeAtlas-Backup-Password': 'Q29udHJhc2XDsWEgc2VndXJhIDIwMjY=' } })
  await saveBackupSchedule({ enabled: false, intervalHours: 24, keep: 7 }, 'unsubmitted password edit')
  expect(fetcher.mock.calls[1]?.[1].headers).not.toHaveProperty('X-HomeAtlas-Backup-Password')
})
it('loads history pages and invokes a persisted schedule without resending its secret', async () => {
  const fetcher = vi.fn().mockImplementation(async () => Response.json({ items: [], total: 0 }))
  vi.stubGlobal('fetch', fetcher)
  await getBackupSchedule(); await getBackupHistory(25); await runBackupSchedule(); await downloadHistoryBackup('an/id')
  expect(fetcher.mock.calls.map(call => call[0])).toEqual(['/api/household/backups/schedule', '/api/household/backups/history?offset=25', '/api/household/backups/schedule/run', '/api/household/backups/history/an%2Fid/download'])
  expect(fetcher.mock.calls[2]?.[1]).toMatchObject({ method: 'POST', credentials: 'same-origin' })
  expect(fetcher.mock.calls[2]?.[1].headers).not.toHaveProperty('X-HomeAtlas-Backup-Password')
})
it('distinguishes retained, expired, missing and failed backups without promising a file is usable', () => {
  const entry = { id: 'a', startedAt: '', trigger: 'scheduled' as const, sizeBytes: 10, status: 'ready' as const, available: true }
  expect(backupHistoryStatus(entry)).toBe('Disponible')
  expect(backupHistoryStatus({ ...entry, available: false })).toBe('Archivo no disponible')
  expect(backupHistoryStatus({ ...entry, status: 'expired' })).toBe('Retirada por retención')
  expect(backupHistoryStatus({ ...entry, status: 'failed' })).toBe('Fallida')
  expect(backupHistoryError('permission_denied')).toContain('permisos')
  expect(backupHistoryError('backup_schedule_key_unavailable')).toContain('clave privada')
  expect(backupHistoryError('backup_retention_failed')).toContain('La copia es válida')
  expect(backupHistoryError('backup_interrupted')).toContain('conservaron')
})
it('propagates permission failures instead of treating a rejected configuration as saved', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: 'permission_denied' }, { status: 403 })))
  await expect(saveBackupSchedule({ enabled: true, intervalHours: 24, keep: 7 }, 'Frase privada 2026')).rejects.toMatchObject({ code: 'permission_denied' })
})
