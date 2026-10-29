import { afterEach, expect, it } from 'vitest'
import { setAuthState } from './auth'
import { formatHouseholdDate } from './datetime'

afterEach(() => setAuthState({ setupRequired: false, session: null }))

it('reflects the household timezone near a date boundary without changing stored timestamps', () => {
  const date = '2026-10-21T02:00:00Z'
  const base = { user: { id: 'alice', name: 'Alice', email: 'alice@example.test' }, role: 'owner', permissions: [], expiresAt: date }
  const household = { id: 'home', name: 'Home', timezone: 'UTC' }
  setAuthState({ setupRequired: false, session: { ...base, household } })
  expect(formatHouseholdDate(date)).toMatch(/\b21\b/)
  setAuthState({ setupRequired: false, session: { ...base, household: { ...household, timezone: 'America/Mexico_City' } } })
  expect(formatHouseholdDate(date)).toMatch(/\b20\b/)
  expect(formatHouseholdDate('invalid')).toBe('Fecha no disponible')
})
