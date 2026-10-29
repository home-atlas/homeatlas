import { ApiError } from './api'
export interface PINState { available: boolean; name?: string; username?: string }
export interface AccessSettings { username: string; hasPassword: boolean; canUsePIN: boolean; devices: { id: string; name: string; expiresAt: string; locked: boolean }[] }
export const validPIN = (pin: string) => /^[0-9]{6,12}$/.test(pin)
export function accessError(cause: unknown) {
  if (!(cause instanceof ApiError)) return 'No pudimos conectar. Comprueba la conexión y vuelve a intentarlo.'
  const messages: Record<string, string> = {
    invalid_pin: 'Escribe un PIN de entre 6 y 12 números y un nombre para tu teléfono o computadora.',
    invalid_username: 'Usa entre 3 y 40 letras sin acentos, números, puntos, guiones o guiones bajos.',
    account_identifier_taken: 'Ese nombre de usuario o correo ya está en uso. Elige otro.',
    invalid_assisted_member: 'Revisa el nombre, el usuario y el correo opcional. El alta asistida es para miembros sin administración.',
    password_confirmation_required: 'Confirma tu contraseña actual para realizar este cambio.',
    pin_not_allowed: 'Las cuentas con administración o acceso a copias utilizan contraseña.',
    enrollment_unavailable: 'Este enlace ya no funciona. Dura 15 minutos y solo se puede usar una vez. Pide uno nuevo para tu misma cuenta.',
    sign_out_before_enrollment: 'Cierra la sesión actual antes de preparar este equipo para otra persona.',
    invalid_grant: 'No puedes conceder ese rol o esos permisos.',
    protected_member: 'Esta cuenta no admite vinculación asistida. Conserva su acceso habitual.',
  }
  return messages[cause.code] ?? (cause.status === 429 ? 'Hay demasiados intentos. Espera unos minutos antes de volver a intentarlo.' : cause.status === 401 ? 'Vuelve a iniciar sesión para continuar.' : cause.status === 403 ? 'No tienes permiso para realizar este cambio.' : 'No pudimos completar el cambio. Comprueba su estado antes de volver a intentarlo.')
}
