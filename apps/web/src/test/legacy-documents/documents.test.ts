import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError, download, patch, upload } from '@/core/api'
import { registerModules } from '@/core/modules'
import { errorMessage, validateFile } from './model'

afterEach(() => vi.unstubAllGlobals())
describe('document boundaries', () => {
  it('sends multipart without overriding its browser boundary and keeps cookies on all operations', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(Response.json({ id: 'a' }, { status: 201 }))
      .mockResolvedValueOnce(Response.json({ id: 'a', title: 'updated' }))
      .mockResolvedValueOnce(new Response('original', { headers: { 'Content-Type': 'text/plain' } }))
    vi.stubGlobal('fetch', fetcher)
    const data = new FormData(); data.append('file', new File(['original'], 'receipt.txt')); data.append('metadata', '{"title":"Receipt","category":"recibos"}')
    await expect(upload('/api/documents', data)).resolves.toEqual({ id: 'a' })
    expect(fetcher.mock.calls[0]?.[1]).toMatchObject({ method: 'POST', body: data, credentials: 'same-origin' })
    expect(fetcher.mock.calls[0]?.[1].headers).not.toHaveProperty('Content-Type')
    await patch('/api/documents/a', { title: 'updated' })
    expect(fetcher.mock.calls[1]?.[1]).toMatchObject({ method: 'PATCH', body: '{"title":"updated"}', credentials: 'same-origin' })
    expect(await (await download('/api/documents/a/download')).text()).toBe('original')
    expect(fetcher.mock.calls[2]?.[1]).toMatchObject({ credentials: 'same-origin' })
  })
  it('does not turn authorization failures into downloadable error files', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ error: 'authentication_required' }, { status: 401 })))
    await expect(download('/api/documents/a/download')).rejects.toMatchObject({ status: 401 })
    expect(errorMessage(new ApiError(404))).toContain('no tienes acceso')
  })
  it('checks file size and extension before uploading', () => {
    expect(validateFile(new File(['x'], 'contract.PDF'))).toBe('')
    expect(validateFile(new File([], 'empty.txt'))).toContain('vacío')
    expect(validateFile(new File(['<html>'], 'bad.html'))).toContain('PDF')
    const file = new File(['x'], 'large.txt'); Object.defineProperty(file, 'size', { value: 20 * 1048576 + 1 })
    expect(validateFile(file)).toContain('20 MiB')
  })
  it('registers detail links only for an active module', async () => {
    for (const enabled of [true, false]) {
      const router = createRouter({ history: createMemoryHistory(), routes: [] })
      await registerModules(router, [{ id: 'documents', name: 'Documents', version: '0.2.0', contractVersion: '1', enabled, available: true, accessPermission: 'documents.read', capabilities: ['documents.read', 'documents.write'], dependencies: [], navigation: { label: 'Documentos', path: '/documents' } }])
      expect(router.hasRoute('document-detail')).toBe(enabled)
      if (enabled) expect(router.resolve('/documents/record').name).toBe('document-detail')
    }
  })
})
