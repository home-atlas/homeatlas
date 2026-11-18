import { ApiError } from './api'
import { sessionScope, type Session } from './auth'

export interface AccountProfile { user: Session['user']; hasPassword: boolean }
export interface AccountSession { id: string; current: boolean; method: 'password' | 'pin'; lastSeenAt: string; expiresAt: string }
export interface AccountSessions { sessions: AccountSession[]; more: boolean }

export const accountContext = sessionScope
export function accountError(cause: unknown) {
  if (cause instanceof ApiError) {
    if (cause.status === 401) return 'Tu sesión ha terminado. Vuelve a iniciar sesión.'
    if (cause.code === 'invalid_profile') return 'Escribe un nombre de entre 1 y 80 caracteres, sin saltos de línea.'
    if (cause.code === 'session_not_found') return 'Ese acceso ya no está activo. Actualiza la lista.'
  }
  return 'No pudimos completar la solicitud. Comprueba la conexión y vuelve a intentarlo.'
}
