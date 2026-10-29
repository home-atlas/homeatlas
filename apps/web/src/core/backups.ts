import { ApiError, checkResponse } from './api'

export const maxBackupBytes = 1024 * 1024 * 1024
export function canBackupHousehold(member: { role: string; permissions: string[] } | null | undefined) {
  return !!member && member.role === 'owner' && ['household.manage', 'household.backup'].every(permission => member.permissions.includes(permission))
}
export function validBackupPassword(password: string) {
  return [...password].length >= 12 && new TextEncoder().encode(password).length <= 256 && password.trim().length > 0
}
function passwordHeader(password: string) {
  return { 'X-HomeAtlas-Backup-Password': btoa(String.fromCharCode(...new TextEncoder().encode(password))) }
}
export async function createBackup(password: string): Promise<Blob> {
  const response = await fetch('/api/household/backups', { method: 'POST', credentials: 'same-origin', headers: { 'X-HomeAtlas-Backup-Version': '4', ...passwordHeader(password) }, signal: AbortSignal.timeout(120000) })
  await checkResponse(response)
  return response.blob()
}
function archiveHeaders(file: File) {
  return { 'Content-Type': 'application/octet-stream', ...(file.name.toLowerCase().endsWith('.zip') ? { 'X-HomeAtlas-Allow-Legacy-Backup': 'true' } : {}) }
}
export async function restoreBackup(file: File, password: string) {
  const response = await fetch('/api/household/restore', {
    method: 'POST', credentials: 'same-origin', body: file, signal: AbortSignal.timeout(120000),
    headers: { ...archiveHeaders(file), 'X-HomeAtlas-Confirm-Restore': 'replace-household', ...passwordHeader(password) },
  })
  await checkResponse(response)
  return response.json() as Promise<{ restored: boolean; moduleRecords: Record<string, number>; documents?: number; previousBackupAvailable: boolean; unavailableOriginals: number; previousUnavailableOriginals: number }>
}
export function backupError(cause: unknown): string {
  if (cause instanceof ApiError) {
    switch (cause.code) {
      case 'authentication_required': return 'Tu sesión caducó. Vuelve a iniciar sesión.'
      case 'permission_denied': return 'Solo el propietario con los permisos de gestión y respaldo puede realizar esta operación.'
      case 'household_busy': return 'Hay otra operación en curso. Espera a que termine y vuelve a intentarlo.'
      case 'backup_household_conflict': return 'La copia pertenece a otro hogar o entra en conflicto con sus registros. No se aplicaron cambios.'
      case 'backup_password_required': return 'Indica la contraseña de la copia. Para un ZIP antiguo o su copia previa, elige una contraseña de al menos 12 caracteres para proteger el respaldo.'
      case 'invalid_backup_password': return 'Usa una contraseña de al menos 12 caracteres y hasta 256 bytes; evita usar solo espacios.'
      case 'backup_decryption_failed': return 'La contraseña no es correcta o la copia cifrada está dañada o incompleta. No se aplicaron cambios.'
      case 'invalid_backup': return 'La copia está dañada, incompleta o usa un formato incompatible. No se aplicaron cambios.'
      case 'invalid_backup_schedule': return 'Elige una frecuencia y conserva entre 1 y 30 copias.'
      case 'backup_schedule_disabled': return 'Activa y guarda la programación antes de crear una copia desde el historial.'
      case 'backup_schedule_key_unavailable': return 'No se pudo abrir la contraseña guardada. Revisa la clave privada del servidor o configura de nuevo la contraseña de las copias automáticas.'
      case 'backup_too_large': return 'La copia supera el límite de 1 GiB.'
      case 'backup_not_found': return 'Aún no hay una copia previa a una restauración para este hogar.'
      case 'backup_original_unavailable': return 'No se pudo crear una copia completa: faltan originales o están dañados. Puedes restaurar una copia válida para recuperarlos.'
      case 'invalid_setup_code': return 'El código de alta no es correcto. Consulta el archivo privado setup-code de esta instalación nueva.'
      case 'setup_completed': return 'Esta instalación ya contiene datos. La recuperación completa solo está disponible antes del alta inicial.'
      case 'backup_module_required': return 'Falta un módulo compatible con los datos de esta copia. Habilítalo en el servidor antes de restaurarla; tus datos actuales se conservan.'
      case 'backup_identity_required': return 'Esta copia antigua no incluye miembros. Necesitas una copia completa en formato v2, v3 o v4 para recuperar una instalación nueva.'
      case 'too_many_attempts': return 'Hay demasiados intentos. Espera antes de volver a intentarlo.'
    }
  }
  return 'No se pudo completar la operación. Comprueba el estado del hogar antes de reintentar; una desconexión no confirma si la restauración terminó.'
}

export async function recoverInstallation(file: File, code: string, password = '') {
  const response = await fetch('/api/setup/restore', {
    method: 'POST', credentials: 'same-origin', body: file, signal: AbortSignal.timeout(120000),
    headers: { ...archiveHeaders(file), 'X-HomeAtlas-Setup-Code': code, 'X-HomeAtlas-Confirm-Restore': 'initialize-installation', ...passwordHeader(password) },
  })
  await checkResponse(response)
  return response.json() as Promise<{ restored: boolean; householdName: string; members: number; moduleRecords: Record<string, number>; documents?: number; unavailableOriginals: number }>
}

export async function downloadPreviousBackup(password = ''): Promise<Blob> {
  const response = await fetch('/api/household/backups/previous', { credentials: 'same-origin', headers: passwordHeader(password), signal: AbortSignal.timeout(120000) })
  await checkResponse(response)
  return response.blob()
}
