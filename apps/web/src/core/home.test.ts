import { expect, it } from 'vitest'
import { moduleLink } from './home'

it('accepts local module links with query state and rejects routes outside the module', () => {
  for (const path of ['/documents', '/documents/a', '/documents?q=manual#results']) expect(moduleLink(path, '/documents')).toBe(true)
  for (const path of ['https://example.test/documents/a', '//example.test/documents', '/documents-other', '/documents/../settings', '/documents/%2e%2e/settings', '/documents/\\example.test', '/documents/\nitem']) expect(moduleLink(path, '/documents')).toBe(false)
})
