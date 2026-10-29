import { ApiError } from './api'
export function canManageHousehold(member: { role: string; permissions: string[] } | null | undefined) {
  return !!member && ['owner', 'admin'].includes(member.role) && member.permissions.includes('household.manage')
}
export interface Member { id: string; name: string; email?: string; username?: string; assisted?: boolean; role: string; permissions: string[] }
export interface Family { members: Member[]; canManage: boolean; roles: string[]; capabilities: string[] }
export interface Invitation { id: string; email: string; role: string; permissions: string[]; expiresAt: string; status: string }
export const roleLabel = (role: string) => ({ owner: 'Propietario', admin: 'Administrador', member: 'Miembro', restricted: 'Restringido', guest: 'Invitado' }[role] ?? role)
export const roleSummary = (role: string) => ({ owner: 'Propietario del hogar.', admin: 'Gestiona el hogar y sus miembros.', member: 'Usa los módulos con los permisos que elijas.', restricted: 'Solo consulta lo que se le autorice.', guest: 'Solo consulta; su acceso no caduca automáticamente.' }[role] ?? '')
export const roleDescription = (role: string) => ({
  owner: 'Es la cuenta del alta inicial. Gestiona el hogar y las copias completas, incluidos datos privados de los módulos. No se asigna mediante una invitación.',
  admin: 'Gestiona el hogar, los módulos y las invitaciones. Puede gestionar miembros de roles inferiores, pero no al propietario ni a otros administradores. No recibe acceso automático a documentos privados.',
  member: 'No administra el hogar. Puede consultar, subir o compartir documentos según los permisos que elijas debajo.',
  restricted: 'Solo puede consultar y descargar documentos autorizados si marcas el permiso de lectura. No puede subir, editar ni compartir.',
  guest: 'Por ahora tiene los mismos límites que Restringido: solo lectura si la autorizas. Este rol no hace que su acceso caduque automáticamente.',
}[role] ?? '')
export const permissionLabel = (permission: string) => ({ 'documents.read': 'Consultar y descargar documentos autorizados', 'documents.write': 'Subir documentos y editar los propios', 'documents.share': 'Compartir documentos propios', 'household.manage': 'Gestionar hogar, módulos, miembros e invitaciones', 'household.backup': 'Crear y restaurar copias del hogar, incluidos datos privados de los módulos' }[permission] ?? permission)
export const permissionDescription = (permission: string) => ({
  'documents.read': 'Ver, buscar y descargar sus documentos y los que otras personas compartan expresamente con ella. No abre todos los archivos del hogar.',
  'documents.write': 'Subir archivos y editar los datos de sus propios documentos. No permite editar documentos recibidos. Incluye el permiso de lectura.',
  'documents.share': 'Dar o retirar acceso de lectura y descarga a sus propios documentos, para miembros concretos del hogar. No permite volver a compartir los que recibe. Incluye el permiso de lectura.',
}[permission] ?? '')
export const statusLabel = (status: string) => ({ pending: 'Pendiente', accepted: 'Aceptada', expired: 'Caducada', revoked: 'Revocada' }[status] ?? status)
export function invitationNotice(status: string) {
  if (status === 'sent') return 'Invitación creada y aceptada por el servidor de correo. La entrega al buzón puede tardar. También puedes compartir el enlace en privado; solo se muestra ahora.'
  if (status === 'failed' || status === 'disabled') return 'Invitación creada, pero no se pudo enviar por correo. Copia el enlace y entrégalo en privado; solo se muestra ahora.'
  return 'Invitación creada. Copia el enlace y entrégalo en privado a la persona invitada. Solo se muestra ahora.'
}
export function rolePermissions(role: string, permissions: string[]) {
  return permissions.filter(p => p !== 'household.manage' && p !== 'household.backup' && (!['guest', 'restricted'].includes(role) || p.endsWith('.read')))
}
export function householdError(error: unknown): string {
  if (!(error instanceof ApiError)) return 'No pudimos conectar con el servidor. Inténtalo de nuevo.'
  const messages: Record<string, string> = {
    assisted_admin_not_allowed: 'Una cuenta asistida con PIN no puede recibir permisos de administración o copias. Primero debe configurar una contraseña mediante recuperación local.',
    already_member: 'Esta cuenta ya es miembro del hogar.', invalid_grant: 'El rol o los permisos elegidos no están permitidos para tu cuenta.',
    protected_member: 'No puedes modificar tu propia cuenta, al propietario o a un administrador de tu mismo nivel.',
    invalid_email: 'Escribe un correo electrónico válido.', invalid_account: 'Revisa el nombre y la contraseña: entre 8 y 128 caracteres.',
    invitation_account_mismatch: 'La invitación corresponde a otra cuenta. Cierra la sesión actual antes de aceptarla.',
    invalid_credentials: 'La contraseña de la cuenta no es correcta.', account_changed: 'La cuenta cambió mientras aceptabas la invitación. Abre de nuevo el enlace.',
    recipient_cannot_read: 'Este miembro necesita permiso de lectura antes de recibir un documento.',
  }
  if (messages[error.code]) return messages[error.code]!
  if (error.status === 401) return 'Vuelve a iniciar sesión para continuar.'
  if (error.status === 403) return 'No tienes permiso para realizar esta acción.'
  if (error.status === 404) return 'El miembro o documento no está disponible en este hogar.'
  if (error.status === 410) return 'La invitación ya no está disponible: puede haber caducado, haberse usado o estar revocada. Solicita una nueva.'
  if (error.status === 429) return 'Hay demasiados intentos. Espera unos minutos e inténtalo de nuevo.'
  return 'No pudimos completar la operación. Revisa los datos e inténtalo de nuevo.'
}

export function togglePermission(current: string[], capability: string, checked: boolean, allowed: string[]) {
  if (checked && !allowed.includes(capability)) return current
  const module = capability.slice(0, capability.lastIndexOf('.'))
  const read = module + '.read'
  const requiresRead = capability.endsWith('.write') || capability.endsWith('.share')
  if (checked && requiresRead && !allowed.includes(read)) return current
  let next = checked ? [...new Set([...current, capability])] : current.filter(p => p !== capability)
  if (capability.endsWith('.read') && !checked) next = next.filter(p => p !== module + '.write' && p !== module + '.share')
  if (checked && requiresRead && !next.includes(read)) next.push(read)
  return next
}
