import { session } from './auth'

export function formatHouseholdDate(value: string, includeTime = false) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'Fecha no disponible'
  const options: Intl.DateTimeFormatOptions = { dateStyle: 'medium', ...(includeTime ? { timeStyle: 'short' } : {}), timeZone: session.value?.household.timezone ?? 'UTC' }
  try { return new Intl.DateTimeFormat('es', options).format(date) }
  catch { return new Intl.DateTimeFormat('es', { ...options, timeZone: 'UTC' }).format(date) + ' UTC' }
}
