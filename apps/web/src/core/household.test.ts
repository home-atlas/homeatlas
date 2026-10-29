import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, post, put, remove } from './api'
import { authRedirect } from './auth'
import { canManageHousehold, householdError, invitationNotice, rolePermissions, togglePermission } from './household'

afterEach(() => vi.unstubAllGlobals())

it('requires both an administrative role and the current household capability', () => {
  for (const role of ['owner', 'admin']) {
    expect(canManageHousehold({ role, permissions: ['household.manage'] })).toBe(true)
    expect(canManageHousehold({ role, permissions: [] })).toBe(false)
  }
  expect(canManageHousehold({ role: 'member', permissions: ['household.manage'] })).toBe(false)
  expect(canManageHousehold(null)).toBe(false)
})

it('preserves manual invitation delivery when SMTP fails or is disabled', () => {
  for (const status of ['failed', 'disabled']) expect(invitationNotice(status)).toContain('Copia el enlace')
  expect(invitationNotice('sent')).toContain('aceptada por el servidor')
  expect(invitationNotice('manual')).toContain('entrégalo en privado')
})
describe('household access flows', () => {
  it('allows an invitation landing page without a session while keeping family private', () => {
    const state = { setupRequired: false, session: null }
    expect(authRedirect({ path: '/join', fullPath: '/join#secret', query: {} }, state)).toBeUndefined()
    expect(authRedirect({ path: '/family', fullPath: '/family', query: {} }, state)).toBe('/login?redirect=%2Ffamily')
    expect(authRedirect({ path: '/join', fullPath: '/join#secret', query: {} }, { setupRequired: true, session: null })).toBe('/setup')
  })
  it('strips management grants and limits guest/restricted roles to read capabilities', () => {
    const all = ['documents.read', 'documents.write', 'documents.share', 'household.manage', 'household.backup']
    expect(rolePermissions('member', all)).toEqual(['documents.read', 'documents.write', 'documents.share'])
    for (const role of ['guest', 'restricted']) expect(rolePermissions(role, all)).toEqual(['documents.read'])
  })
  it('lets an administrator revoke an existing permission they cannot grant, and keeps read dependencies', () => {
    const allowed = ['documents.read']
    expect(togglePermission(['documents.read'], 'documents.write', true, allowed)).toEqual(['documents.read'])
    expect(togglePermission(['documents.read', 'documents.write'], 'documents.write', false, allowed)).toEqual(['documents.read'])
    expect(togglePermission([], 'documents.share', true, ['documents.read', 'documents.share'])).toEqual(['documents.share', 'documents.read'])
    expect(togglePermission(['documents.read', 'documents.write'], 'documents.read', false, allowed)).toEqual([])
  })
  it('keeps read dependencies scoped to their module and rejects incomplete grants', () => {
    const allowed = ['documents.read', 'documents.write', 'inventory.read', 'inventory.write', 'inventory.share']
    expect(togglePermission(['documents.read'], 'inventory.write', true, allowed)).toEqual(['documents.read', 'inventory.write', 'inventory.read'])
    expect(togglePermission(['documents.read', 'documents.write', 'inventory.read', 'inventory.share'], 'inventory.read', false, allowed)).toEqual(['documents.read', 'documents.write'])
    expect(togglePermission(['documents.read'], 'inventory.write', true, ['inventory.write'])).toEqual(['documents.read'])
    expect(togglePermission(['documents.read'], 'documents.read', true, allowed)).toEqual(['documents.read'])
  })
  it('keeps invitation tokens in JSON bodies and sends cookies on share/revoke', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(Response.json({ householdName: 'Home' }))
      .mockResolvedValueOnce(new Response(null, { status: 204 })).mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetcher)
    await post('/api/invitations/inspect', { token: 'private-invitation' })
    expect(fetcher.mock.calls[0]?.[0]).toBe('/api/invitations/inspect')
    expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ method: 'POST', credentials: 'same-origin', body: '{"token":"private-invitation"}' })
    await put('/api/documents/a/shares/b'); await remove('/api/documents/a/shares/b')
    expect(fetcher.mock.calls[1]?.[1]).toMatchObject({ method: 'PUT', credentials: 'same-origin' })
    expect(fetcher.mock.calls[2]?.[1]).toMatchObject({ method: 'DELETE', credentials: 'same-origin' })
  })
  it('explains unavailable invitations and account mismatch without suggesting a password reset', () => {
    expect(householdError(new ApiError(410, 'invitation_unavailable'))).toContain('Solicita una nueva')
    expect(householdError(new ApiError(403, 'invitation_account_mismatch'))).toContain('otra cuenta')
  })
})
