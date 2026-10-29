import { afterEach, expect, it, vi } from 'vitest'
import { ApiError } from './api'
import { backupError, canBackupHousehold, createBackup, recoverInstallation, restoreBackup, downloadPreviousBackup, validBackupPassword } from './backups'

afterEach(() => vi.unstubAllGlobals())
it('reserves household backups to owners with both explicit capabilities', () => {
  const permissions = ['household.manage', 'household.backup']
  expect(canBackupHousehold({ role: 'owner', permissions })).toBe(true)
  for (const role of ['admin', 'member', 'guest']) expect(canBackupHousehold({ role, permissions })).toBe(false)
  for (const missing of permissions) expect(canBackupHousehold({ role: 'owner', permissions: permissions.filter(p => p !== missing) })).toBe(false)
  expect(canBackupHousehold(null)).toBe(false)
})
it('sends explicit restoration intent and the archive without credentials in URLs', async () => {
  const file = new File(['archive'], 'household.zip', { type: 'application/zip' })
  const fetcher = vi.fn().mockResolvedValueOnce(new Response('zip')).mockResolvedValueOnce(Response.json({ restored: true, documents: 2 }))
  vi.stubGlobal('fetch', fetcher)
  expect(await (await createBackup('Contraseña segura 2026')).text()).toBe('zip')
  await restoreBackup(file, 'Contraseña segura 2026')
  expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ method: 'POST', credentials: 'same-origin', headers: { 'X-HomeAtlas-Backup-Version': '4', 'X-HomeAtlas-Backup-Password': 'Q29udHJhc2XDsWEgc2VndXJhIDIwMjY=' } })
  expect(fetcher.mock.calls[1]?.[0]).toBe('/api/household/restore')
  expect(fetcher.mock.calls[1]?.[1]).toMatchObject({ method: 'POST', credentials: 'same-origin', body: file, headers: { 'X-HomeAtlas-Confirm-Restore': 'replace-household', 'Content-Type': 'application/octet-stream' } })
})
it('keeps the new installation code out of URLs and requires explicit recovery intent', async () => {
  const file = new File(['archive'], 'household.zip', { type: 'application/zip' })
  const fetcher = vi.fn().mockResolvedValue(Response.json({ restored: true, householdName: 'Casa', members: 2, documents: 1 }))
  vi.stubGlobal('fetch', fetcher)
  await recoverInstallation(file, 'local-installation-code')
  expect(fetcher.mock.calls[0]?.[0]).toBe('/api/setup/restore')
  expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ method: 'POST', credentials: 'same-origin', body: file, headers: { 'X-HomeAtlas-Setup-Code': 'local-installation-code', 'X-HomeAtlas-Confirm-Restore': 'initialize-installation' } })
  expect(backupError(new ApiError(400, 'backup_identity_required'))).toContain('formato v2, v3 o v4')
  expect(backupError(new ApiError(409, 'setup_completed'))).toContain('antes del alta')
})
it('reports integrity and authorization failures and keeps uncertain outcomes explicit', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: 'invalid_backup' }, { status: 400 })))
  await expect(createBackup('Contraseña segura 2026')).rejects.toMatchObject({ code: 'invalid_backup' })
  expect(backupError(new ApiError(400, 'invalid_backup'))).toContain('No se aplicaron cambios')
  expect(backupError(new ApiError(403, 'permission_denied'))).toContain('propietario')
  expect(backupError(new Error('network'))).toContain('no confirma')
})

it('validates password length in characters and UTF-8 bytes without normalizing it', () => {
  expect(validBackupPassword('Una frase segura de 2026')).toBe(true)
  expect(validBackupPassword('corta')).toBe(false)
  expect(validBackupPassword(' '.repeat(12))).toBe(false)
  expect(validBackupPassword('á'.repeat(128))).toBe(true)
  expect(validBackupPassword('á'.repeat(129))).toBe(false)
})
it('sends an encryption password for legacy previous copies without putting it in URLs', async () => {
  const fetcher = vi.fn().mockResolvedValue(new Response('encrypted'))
  vi.stubGlobal('fetch', fetcher)
  await downloadPreviousBackup('Contraseña segura 2026')
  expect(fetcher.mock.calls[0]?.[0]).toBe('/api/household/backups/previous')
  expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ headers: { 'X-HomeAtlas-Backup-Password': 'Q29udHJhc2XDsWEgc2VndXJhIDIwMjY=' } })
  expect(backupError(new ApiError(400, 'backup_decryption_failed'))).toContain('No se aplicaron cambios')
})

it('never opts into unencrypted legacy import for an age archive', async () => {
  const file = new File(['ciphertext'], 'household.zip.age')
  const fetcher = vi.fn().mockImplementation(async () => Response.json({ restored: true }))
  vi.stubGlobal('fetch', fetcher)
  await restoreBackup(file, 'Contraseña segura 2026')
  await recoverInstallation(file, 'new-code', 'Contraseña segura 2026')
  for (const call of fetcher.mock.calls) expect(call[1].headers).not.toHaveProperty('X-HomeAtlas-Allow-Legacy-Backup')
  await restoreBackup(new File(['zip'], 'historical.zip'), 'Contraseña segura 2026')
  expect(fetcher.mock.calls[2]?.[1].headers).toHaveProperty('X-HomeAtlas-Allow-Legacy-Backup','true')
})
