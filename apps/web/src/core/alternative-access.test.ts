import { expect, it } from 'vitest'
import { ApiError } from './api'
import { accessError, validPIN } from './alternative-access'
it('accepts complete numeric PINs and rejects short, spaced or nonnumeric values', () => {
  for (const value of ['642817', '123456789012']) expect(validPIN(value)).toBe(true)
  for (const value of ['', '12345', '1234567890123', ' 642817', '642817 ', '123a56', '１２３４５６']) expect(validPIN(value)).toBe(false)
})
it('explains device binding, administrator proof and expired assistance without exposing credentials', () => {
  expect(accessError(new ApiError(403, 'password_confirmation_required'))).toContain('contraseña actual')
  expect(accessError(new ApiError(410, 'enrollment_unavailable'))).toContain('Pide uno nuevo')
  expect(accessError(new ApiError(403, 'pin_not_allowed'))).toContain('administración')
  expect(accessError(new ApiError(409, 'sign_out_before_enrollment'))).toContain('Cierra la sesión')
  expect(accessError(new ApiError(429, 'too_many_attempts'))).toContain('Espera')
})
