import { computed, shallowRef } from 'vue'
import { get, post } from './api'

export interface Session {
  user: { id: string; name: string; email: string; username?: string; emailVerified?: boolean }
  household: { id: string; name: string; timezone: string }
  role: string
  permissions: string[]
  expiresAt: string
}

export interface AuthState {
  setupRequired: boolean
  session: Session | null
  smtpEnabled?: boolean
}

const state = shallowRef<AuthState>({ setupRequired: false, session: null })
let revision = 0
export const session = computed(() => state.value.session)
export const smtpEnabled = computed(() => state.value.smtpEnabled === true)

export function setAuthState(next: AuthState) { revision++; state.value = next }

export async function refreshSession() {
  const startedAt = revision
  const next = await get<AuthState>('/api/auth/session')
  if (revision === startedAt) setAuthState(next)
  return state.value
}

export async function logout() {
  revision++
  await post('/api/auth/logout')
  setAuthState({ setupRequired: false, session: null })
}

export function safeReturnTo(value: unknown): string {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || /[\\\u0000-\u001f\u007f]/.test(value)) return '/'
  try {
    const url = new URL(value, 'https://homeatlas.invalid')
    if (url.origin !== 'https://homeatlas.invalid' || ['/login', '/setup', '/forgot-password', '/activate-access', '/recover', '/verify-email'].includes(url.pathname)) return '/'
    return url.pathname + url.search + url.hash
  } catch { return '/' }
}

export function authRedirect(to: { path: string; fullPath: string; query: { redirect?: unknown } }, next: AuthState): string | undefined {
  if (next.setupRequired) return to.path === '/setup' ? undefined : '/setup'
  if (to.path === '/join' || to.path === '/activate-access' || to.path === '/forgot-password' || to.path === '/recover' || to.path === '/verify-email') return
  if (!next.session) {
    return to.path === '/login' ? undefined : '/login?redirect=' + encodeURIComponent(safeReturnTo(to.fullPath))
  }
  if (to.path === '/setup' || to.path === '/login') return safeReturnTo(to.query.redirect)
}
