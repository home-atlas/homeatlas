import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, get, post } from './api'
import { authRedirect, logout, refreshSession, safeReturnTo, session, setAuthState, smtpEnabled, type AuthState } from './auth'

const signedOut: AuthState = { setupRequired: false, session: null }
const signedIn: AuthState = {
  setupRequired: false,
  session: {
    user: { id: 'alice', name: 'Alice', email: 'alice@example.test' },
    household: { id: 'home', name: 'Nuestro hogar', timezone: 'UTC' },
    role: 'owner', permissions: ['documents.read'], expiresAt: '2026-10-22T00:00:00Z',
  },
}

beforeEach(() => { setAuthState(signedOut) })
afterEach(() => { vi.unstubAllGlobals() })

describe('authentication navigation', () => {
  it('keeps SMTP disabled unless explicitly reported by the server and permits email proof without login', () => {
    expect(smtpEnabled.value).toBe(false)
    setAuthState({ ...signedOut, smtpEnabled: true })
    expect(smtpEnabled.value).toBe(true)
    const verification = { path: '/verify-email', fullPath: '/verify-email#private-proof', query: {} }
    expect(authRedirect(verification, signedOut)).toBeUndefined()
    expect(authRedirect(verification, { setupRequired: true, session: null })).toBe('/setup')
    expect(safeReturnTo('/verify-email#private-proof')).toBe('/')
  })
  const documents = { path: '/documents', fullPath: '/documents', query: {} }
  it('requires setup first and login for all private routes', () => {
    expect(authRedirect(documents, { setupRequired: true, session: null })).toBe('/setup')
    expect(authRedirect({ path: '/setup', fullPath: '/setup', query: {} }, { setupRequired: true, session: null })).toBeUndefined()
    expect(authRedirect(documents, signedOut)).toBe('/login?redirect=%2Fdocuments')
    expect(authRedirect(documents, signedIn)).toBeUndefined()
    expect(authRedirect({ path: '/login', fullPath: '/login?redirect=%2Fdocuments', query: { redirect: '/documents' } }, signedIn)).toBe('/documents')
  })

  it('rejects external return URLs and authentication loops', () => {
    for (const value of ['https://evil.test', '//evil.test', '/\\evil.test', '/login', '/setup', '/forgot-password', '/forgot-password#private-code', '/activate-access#private-code', '/recover', '/a/../login', '/\n/evil.test', ['/documents'], null]) {
      expect(safeReturnTo(value)).toBe('/')
    }
    expect(safeReturnTo('/documents?category=manuales#library')).toBe('/documents?category=manuales#library')
  })

  it('allows local recovery without a session only after installation setup', () => {
    for (const path of ['/forgot-password', '/recover', '/activate-access']) {
      const recovery = { path, fullPath: path, query: {} }
      expect(authRedirect(recovery, signedOut)).toBeUndefined()
      expect(authRedirect(recovery, signedIn)).toBeUndefined()
      expect(authRedirect(recovery, { setupRequired: true, session: null })).toBe('/setup')
    }
  })
})

describe('session lifecycle', () => {
  it('uses same-origin HttpOnly cookie transport and handles empty logout responses', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetch)
    setAuthState(signedIn)
    await logout()
    expect(session.value).toBeNull()
    expect(fetch).toHaveBeenCalledWith('/api/auth/logout', expect.objectContaining({
      method: 'POST', credentials: 'same-origin', headers: expect.objectContaining({ 'Content-Type': 'application/json' }), body: '{}',
    }))
  })

  it('keeps state until the server confirms logout', async () => {
    setAuthState(signedIn)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"error":"logout_unavailable"}', { status: 503 })))
    await expect(logout()).rejects.toBeInstanceOf(ApiError)
    expect(session.value?.user.id).toBe('alice')
  })

  it('discards an in-flight session response after logout', async () => {
    setAuthState(signedIn)
    let resolveSession!: (response: Response) => void
    vi.stubGlobal('fetch', vi.fn()
      .mockImplementationOnce(() => new Promise<Response>(resolve => { resolveSession = resolve }))
      .mockResolvedValueOnce(new Response(null, { status: 204 })))
    const pending = refreshSession()
    await logout()
    resolveSession(new Response(JSON.stringify(signedIn)))
    await pending
    expect(session.value).toBeNull()
  })

  it('clears an expired session and preserves server error codes', async () => {
    setAuthState(signedIn)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(signedOut))))
    await refreshSession()
    expect(session.value).toBeNull()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"error":"too_many_attempts"}', { status: 429, headers: { 'Retry-After': '900' } })))
    await expect(post('/api/auth/login', { email: 'alice@example.test', password: 'wrong' })).rejects.toMatchObject({ status: 429, code: 'too_many_attempts', retryAfter: 900 })
  })

  it('includes cookies on read requests without an authorization token', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response('[]'))
    vi.stubGlobal('fetch', fetch)
    await get('/api/documents')
    expect(fetch).toHaveBeenCalledWith('/api/documents', expect.objectContaining({ credentials: 'same-origin', method: 'GET', headers: { Accept: 'application/json' } }))
  })
})
