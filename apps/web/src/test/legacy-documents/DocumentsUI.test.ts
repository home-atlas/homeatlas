// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createApp, nextTick, type Component } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError, download, get, patch, put, upload } from '@/core/api'
import { setAuthState } from '@/core/auth'
import DocumentsView from './DocumentsView.vue'
import DocumentDrawer from './DocumentDrawer.vue'
import DocumentDetailView from './DocumentDetailView.vue'
import SharingDialog from './SharingDialog.vue'
import type { Document } from './model'
vi.mock('@/core/api', async original => ({ ...await original<typeof import('@/core/api')>(), get: vi.fn(), download: vi.fn(), patch: vi.fn(), upload: vi.fn(), put: vi.fn(), remove: vi.fn() }))
const apps: { unmount: () => void }[] = []
let activeRouter: ReturnType<typeof createRouter>
const record: Document = { id: 'doc-1', title: 'Manual de prueba', category: 'manuales', description: 'Datos de prueba', tags: ['cocina'], ownerId: 'owner', ownerName: 'Ana', createdAt: '2026-10-21T12:00:00Z', filename: 'manual.txt', mediaType: 'text/plain', size: 10, sha256: 'checksum-test', hasFile: true }
function login(permissions = ['documents.read', 'documents.write', 'documents.share']) { setAuthState({ setupRequired: false, session: { user: { id: 'owner', name: 'Ana', email: '' }, household: { id: 'home', name: 'Casa', timezone: 'UTC' }, role: 'owner', permissions, expiresAt: '2026-10-22T00:00:00Z' } }) }
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount(component: Component, props: Record<string, unknown> = {}, path = '/documents') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/documents', component: { template: '<p />' } }, { path: '/documents/:id', component: { template: '<p />' } }, { path: '/login', component: { template: '<p />' } }] })
  activeRouter = router
  await router.push(path)
  const root = document.createElement('div'), app = createApp(component, props).use(router)
  document.body.append(root); apps.push(app); app.mount(root); await flush()
  return root
}
function input(id: string, value: string) { const field = document.getElementById(id) as HTMLInputElement; field.value = value; field.dispatchEvent(new Event('input', { bubbles: true })) }
function button(text: string, scope: ParentNode = document) { return [...scope.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.trim() === text)! }
async function select(id: string, label: string) {
  document.getElementById(id)!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); await flush()
  const option = [...document.querySelectorAll<HTMLElement>('[role=option]')].find(option => option.textContent?.trim() === label)!
  expect(option).toBeDefined(); option.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); await flush()
}
function submit(scope: ParentNode = document) { scope.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) }
function drag(target: Element, type: string, files: File[] = [], types = ['Files']) {
  const event = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'dataTransfer', { value: { files, types, dropEffect: 'none' } })
  target.dispatchEvent(event)
  return event
}
const dropzone = () => document.querySelector('[role=group][aria-labelledby=document-file-label]')!
beforeEach(() => { login(); vi.mocked(get).mockResolvedValue([record]) })
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.resetAllMocks(); setAuthState({ setupRequired: false, session: null }); document.body.innerHTML = '' })
it('filters the library with shadcn fields and restores the all-categories filter', async () => {
  const root = await mount(DocumentsView)
  expect(root.querySelector('#document-query')?.getAttribute('data-slot')).toBe('input')
  expect(root.querySelector('select')).toBeNull()
  expect(root.textContent).toContain('Manual de prueba')
  await select('category-filter', 'Contratos'); input('document-query', '  recibo  '); submit(root); await flush()
  expect(get).toHaveBeenLastCalledWith('/api/documents?q=recibo&category=contratos&offset=0')
  await select('category-filter', 'Todas las categorías'); submit(root); await flush()
  expect(get).toHaveBeenLastCalledWith('/api/documents?q=recibo&category=&offset=0')
})
it('keeps reader-only access and shows load failures with the shared alert', async () => {
  login(['documents.read']); vi.mocked(get).mockRejectedValueOnce(new ApiError(403))
  const root = await mount(DocumentsView)
  expect(root.querySelector('[role=alert]')?.getAttribute('data-slot')).toBe('alert')
  expect(root.textContent).not.toContain('Subir documento')
  button('Reintentar', root).click(); await flush()
  expect(root.textContent).toContain('Manual de prueba')
})
it('uploads the chosen file and immediate field values through the shared sheet', async () => {
  const saved = vi.fn(); vi.mocked(upload).mockResolvedValue(record)
  await mount(DocumentDrawer, { onSaved: saved })
  const dialog = document.querySelector('[role=dialog]')!
  expect(dialog.getAttribute('data-slot')).toBe('sheet-content')
  const field = document.getElementById('document-file') as HTMLInputElement
  expect(field.getAttribute('data-slot')).toBe('input'); expect(field.classList.contains('form-file')).toBe(true)
  const file = new File(['contenido'], 'manual.txt', { type: 'text/plain' })
  Object.defineProperty(field, 'files', { value: [file], configurable: true })
  field.dispatchEvent(new Event('input', { bubbles: true })); field.dispatchEvent(new Event('change', { bubbles: true })); await flush()
  expect((document.getElementById('document-title') as HTMLInputElement).value).toBe('manual')
  await select('document-category', 'Recibos')
  input('document-title', '  Factura  '); input('document-description', '  Detalle nuevo  '); input('document-tags', ' casa, cocina ')
  submit(dialog); await flush()
  const body = vi.mocked(upload).mock.calls[0]![1] as FormData
  expect(body.get('file')).toBeInstanceOf(File)
  expect((body.get('file') as File).name).toBe('manual.txt')
  expect(JSON.parse(String(body.get('metadata')))).toEqual({ title: 'Factura', category: 'recibos', description: 'Detalle nuevo', tags: ['casa', 'cocina'] })
  expect(saved).toHaveBeenCalledWith(record)
})
it('edits metadata with the shared textarea and blocks closing while saving', async () => {
  let complete!: (value: unknown) => void
  vi.mocked(patch).mockImplementationOnce(() => new Promise(resolve => { complete = resolve }))
  const close = vi.fn(); await mount(DocumentDrawer, { document: record, onClose: close })
  expect(document.querySelector('#document-file')).toBeNull()
  expect(document.getElementById('document-description')?.getAttribute('data-slot')).toBe('textarea')
  input('document-description', 'Una descripción nueva'); submit(); await flush()
  expect(patch).toHaveBeenCalledWith('/api/documents/doc-1', expect.objectContaining({ description: 'Una descripción nueva' }))
  expect(button('Guardar cambios')).toBeUndefined()
  expect(button('Guardando…').disabled).toBe(true)
  expect(button('Cancelar').disabled).toBe(true)
  document.querySelector('[role=dialog]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); await flush()
  expect(close).not.toHaveBeenCalled()
  complete(record); await flush()
})
it('does not upload invalid files or allow writes for a reader', async () => {
  await mount(DocumentDrawer)
  const field = document.getElementById('document-file')!
  Object.defineProperty(field, 'files', { value: [new File(['html'], 'pagina.html')] })
  field.dispatchEvent(new Event('change', { bubbles: true })); submit(); await flush()
  expect(document.querySelector('[role=alert]')?.textContent).toContain('PDF')
  expect(upload).not.toHaveBeenCalled()
  login(['documents.read']); submit(); await flush()
  expect(upload).not.toHaveBeenCalled(); expect(button('Guardar documento').disabled).toBe(true)
})
it('shares with an eligible member selected through shadcn without offering non-readers', async () => {
  vi.mocked(get).mockImplementation(async path => path === '/api/household/members' ? { members: [{ id: 'owner', name: 'Ana', permissions: ['documents.read'] }, { id: 'reader', name: 'María', permissions: ['documents.read'] }, { id: 'blocked', name: 'Sin permiso', permissions: [] }] } : [])
  await mount(SharingDialog, { id: 'doc-1' })
  document.getElementById('share-member')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); await flush()
  const options = [...document.querySelectorAll<HTMLElement>('[role=option]')]
  expect(options.map(option => option.textContent?.trim())).toEqual(['María'])
  options[0]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); await flush()
  submit(document.querySelector('[role=dialog]')!); await flush()
  expect(put).toHaveBeenCalledWith('/api/documents/doc-1/shares/reader')
  expect(document.querySelector('#share-member')?.textContent).toContain('Selecciona un miembro')
})
it('keeps metadata and sharing actions hidden on a document shared with a reader', async () => {
  login(['documents.read']); vi.mocked(get).mockResolvedValue({ ...record, ownerId: 'someone-else', ownerName: 'Otra persona' })
  const root = await mount(DocumentDetailView, {}, '/documents/doc-1')
  expect(root.textContent).not.toContain('Editar metadatos'); expect(root.textContent).not.toContain('Gestionar acceso')
  expect(root.textContent).toContain('Compartido contigo')
  expect(root.textContent).not.toContain('checksum-test')
  button('Integridad del archivo', root).click(); await flush()
  expect(root.textContent).toContain('checksum-test')
})
it('drops a file without uploading automatically and submits it without native file-input validation blocking', async () => {
  vi.mocked(upload).mockResolvedValue(record)
  await mount(DocumentDrawer)
  expect(button('Guardar documento').disabled).toBe(true)
  const file = new File(['contenido'], 'manual.txt', { type: 'text/plain' })
  expect(drag(dropzone(), 'drop', [file]).defaultPrevented).toBe(true); await flush()
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('manual.txt')
  expect(document.querySelector('[role=status]')?.textContent).toContain('Listo para guardar')
  expect(button('Guardar documento').disabled).toBe(false)
  expect((document.getElementById('document-file') as HTMLInputElement).required).toBe(false)
  expect(document.querySelector('form')!.checkValidity()).toBe(true)
  expect(upload).not.toHaveBeenCalled()
  submit(); await flush()
  const data = vi.mocked(upload).mock.calls[0]![1] as FormData
  expect((data.get('file') as File).name).toBe('manual.txt')
  expect(JSON.parse(String(data.get('metadata'))).title).toBe('manual')
})
it('keeps the drag indicator stable over child elements and ignores dragged text', async () => {
  await mount(DocumentDrawer)
  const zone = dropzone(), child = zone.querySelector('[data-slot=card-content]')!
  drag(zone, 'dragenter', [], ['text/plain']); await flush()
  expect(zone.hasAttribute('data-dragging')).toBe(false)
  drag(zone, 'dragenter'); drag(child, 'dragenter'); await flush()
  expect(zone.getAttribute('data-dragging')).toBe('true')
  expect(zone.textContent).toContain('Suelta el archivo aquí')
  drag(child, 'dragleave'); await flush(); expect(zone.getAttribute('data-dragging')).toBe('true')
  drag(zone, 'dragleave'); await flush(); expect(zone.hasAttribute('data-dragging')).toBe(false)
})
it.each(['multiple', 'empty', 'large', 'format'])('rejects an invalid %s drop with an inline error and no upload', async kind => {
  await mount(DocumentDrawer)
  let files = [new File(['x'], 'manual.txt')]
  if (kind === 'multiple') files.push(new File(['y'], 'otro.txt'))
  if (kind === 'empty') files = [new File([], 'vacio.txt')]
  if (kind === 'large') Object.defineProperty(files[0], 'size', { value: 20 * 1048576 + 1 })
  if (kind === 'format') files = [new File(['html'], 'pagina.html')]
  drag(dropzone(), 'dragenter'); drag(dropzone(), 'drop', files); await flush()
  expect(dropzone().hasAttribute('data-dragging')).toBe(false)
  expect(document.getElementById('file-error')?.getAttribute('role')).toBe('alert')
  expect(button('Guardar documento').disabled).toBe(true)
  expect(document.querySelector('[role=status]')).toBeNull()
  submit(); await flush(); expect(upload).not.toHaveBeenCalled()
})
it('clears or changes files while preserving a title written by the person', async () => {
  await mount(DocumentDrawer)
  drag(dropzone(), 'drop', [new File(['a'], 'primero.txt')]); await flush()
  expect((document.getElementById('document-title') as HTMLInputElement).value).toBe('primero')
  drag(dropzone(), 'drop', [new File(['b'], 'segundo.txt')]); await flush()
  expect((document.getElementById('document-title') as HTMLInputElement).value).toBe('segundo')
  input('document-title', 'Mi título'); await flush()
  document.querySelector<HTMLButtonElement>('[aria-label="Quitar archivo"]')!.click(); await flush()
  expect((document.getElementById('document-title') as HTMLInputElement).value).toBe('Mi título')
  expect((document.getElementById('document-file') as HTMLInputElement).value).toBe('')
  expect(document.activeElement).toBe(document.getElementById('document-choose-file'))
  drag(dropzone(), 'drop', [new File(['b'], 'segundo.txt')]); await flush()
  expect((document.getElementById('document-title') as HTMLInputElement).value).toBe('Mi título')
  expect(button('Guardar documento').disabled).toBe(false)
})
it('blocks new drops during an upload and after losing write permission', async () => {
  let complete!: (value: unknown) => void
  vi.mocked(upload).mockImplementationOnce(() => new Promise(resolve => { complete = resolve }))
  await mount(DocumentDrawer)
  drag(dropzone(), 'drop', [new File(['a'], 'primero.txt')]); await flush(); submit(); await flush()
  drag(dropzone(), 'drop', [new File(['b'], 'segundo.txt')]); await flush()
  expect(dropzone().textContent).toContain('primero.txt'); expect(dropzone().textContent).not.toContain('segundo.txt')
  expect(button('Cambiar archivo').disabled).toBe(true)
  complete(record); await flush()
  login(['documents.read']); await flush()
  drag(dropzone(), 'drop', [new File(['b'], 'segundo.txt')]); await flush()
  expect(dropzone().textContent).not.toContain('segundo.txt')
  expect(button('Guardar documento').disabled).toBe(true)
})

async function menu(id = 'doc-1', view = 'cards') {
  document.getElementById('document-actions-' + view + '-' + id)!.click(); await flush()
  return document.querySelector('[role=menu]')!
}
function menuItem(name: string) { return [...document.querySelectorAll<HTMLElement>('[role=menuitem]')].find(item => item.textContent?.trim() === name)! }
async function switchView(name: string) { button(name).dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 })); await flush() }
it('switches cards/list without fetching again or losing a search and category', async () => {
  const root = await mount(DocumentsView)
  expect(root.querySelector('[aria-label="Documentos en tarjetas"]')).not.toBeNull()
  input('document-query', 'manual'); await select('category-filter', 'Manuales'); submit(root); await flush()
  const calls = vi.mocked(get).mock.calls.length
  await switchView('Lista')
  expect(root.querySelector('[aria-label="Documentos en lista"]')).not.toBeNull()
  expect(root.querySelector('[aria-label="Documentos en tarjetas"]')).toBeNull()
  expect((document.getElementById('document-query') as HTMLInputElement).value).toBe('manual')
  expect(document.getElementById('category-filter')?.textContent).toContain('Manuales')
  expect(get).toHaveBeenCalledTimes(calls)
  await switchView('Tarjetas'); expect(root.textContent).toContain(record.title)
})
it('opens metadata from its menu and restores focus when cancelled', async () => {
  await mount(DocumentsView)
  await menu(); menuItem('Editar metadatos').click(); await flush()
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('Editar metadatos')
  expect((document.getElementById('document-title') as HTMLInputElement).value).toBe(record.title)
  button('Cancelar').click(); await flush(); await flush()
  expect(document.activeElement).toBe(document.getElementById('document-actions-cards-doc-1'))
})
it('refreshes metadata under the current filters after editing from the list menu', async () => {
  const changed = { ...record, title: 'Manual actualizado' }; vi.mocked(patch).mockResolvedValue(changed)
  const root = await mount(DocumentsView); await switchView('Lista')
  input('document-query', 'manual'); submit(root); await flush()
  await menu('doc-1', 'list'); menuItem('Editar metadatos').click(); await flush()
  input('document-title', changed.title); vi.mocked(get).mockResolvedValue([changed]); submit(document.querySelector('[role=dialog]')!); await flush()
  expect(patch).toHaveBeenCalledWith('/api/documents/doc-1', expect.objectContaining({ title: changed.title }))
  expect(get).toHaveBeenLastCalledWith('/api/documents?q=manual&category=&offset=0')
  expect(document.querySelector('[role=dialog]')).toBeNull(); expect(root.textContent).toContain(changed.title)
})
it('offers sharing only to the document owner with the explicit share permission', async () => {
  await mount(DocumentsView)
  expect((await menu()).textContent).toContain('Gestionar acceso')
  vi.mocked(get).mockImplementation(async path => path === '/api/household/members' ? { members: [] } : path.endsWith('/shares') ? [] : [record])
  menuItem('Gestionar acceso').click(); await flush()
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('Acceso al documento')
  document.querySelector<HTMLButtonElement>('[role=dialog] [aria-label="Cerrar"]')!.click(); await flush(); await flush()
  expect(document.activeElement).toBe(document.getElementById('document-actions-cards-doc-1'))
  login(['documents.read', 'documents.write']); await flush()
  expect((await menu()).textContent).not.toContain('Gestionar acceso')
  expect(document.querySelector('[role=menu]')?.textContent).toContain('Editar metadatos')
})
it('never offers editing or resharing received documents even to an owner with all capabilities', async () => {
  vi.mocked(get).mockResolvedValue([{ ...record, ownerId: 'another-member', ownerName: 'Otra persona' }])
  const root = await mount(DocumentsView)
  expect(root.textContent).toContain('Compartido contigo')
  const actions = await menu()
  expect(actions.textContent).toContain('Ver detalle'); expect(actions.textContent).toContain('Descargar original')
  expect(actions.textContent).not.toContain('Editar metadatos'); expect(actions.textContent).not.toContain('Gestionar acceso')
})
it('disables downloading historical records without an original', async () => {
  vi.mocked(get).mockResolvedValue([{ ...record, hasFile: false }])
  await mount(DocumentsView); await menu()
  expect(menuItem('Descargar original').getAttribute('aria-disabled')).toBe('true')
  expect(download).not.toHaveBeenCalled()
})
it('does not turn download errors into files', async () => {
  const create = vi.fn(); vi.stubGlobal('URL', class extends URL { static createObjectURL = create; static revokeObjectURL = vi.fn() })
  vi.mocked(download).mockRejectedValue(new ApiError(404))
  const root = await mount(DocumentsView); await menu(); menuItem('Descargar original').click(); await flush()
  expect(download).toHaveBeenCalledWith('/api/documents/doc-1/download')
  expect(root.querySelector('[role=alert]')?.textContent).toContain('no tienes acceso')
  expect(create).not.toHaveBeenCalled()
})
it('discards delayed records and downloads when the session or permissions change', async () => {
  let deliver!: (value: unknown) => void
  vi.mocked(get).mockImplementationOnce(() => new Promise(resolve => { deliver = resolve }))
  const root = await mount(DocumentsView); setAuthState({ setupRequired: false, session: null }); deliver([record]); await flush()
  expect(root.textContent).not.toContain(record.title)
  login(); await flush()
  let finish!: (value: Blob) => void
  vi.mocked(download).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  const create = vi.fn(); vi.stubGlobal('URL', class extends URL { static createObjectURL = create; static revokeObjectURL = vi.fn() })
  await menu(); menuItem('Descargar original').click(); await flush()
  login([]); finish(new Blob(['private'])); await flush()
  expect(root.textContent).not.toContain(record.title); expect(create).not.toHaveBeenCalled()
})

it('downloads the original filename from the menu without navigating away', async () => {
  const create = vi.fn().mockReturnValue('blob:test-document')
  vi.stubGlobal('URL', class extends URL { static createObjectURL = create; static revokeObjectURL = vi.fn() })
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    expect(this.download).toBe(record.filename); expect(this.href).toBe('blob:test-document')
  })
  vi.mocked(download).mockResolvedValue(new Blob(['original']))
  const root = await mount(DocumentsView); await menu(); menuItem('Descargar original').click(); await flush()
  expect(create).toHaveBeenCalledOnce(); expect(click).toHaveBeenCalledOnce()
  expect(root.textContent).toContain(record.title)
  expect(root.querySelector('[role=alert]')).toBeNull()
})

it('opens owner actions from the detail menu and returns focus after editing', async () => {
  vi.mocked(get).mockImplementation(async path => path.endsWith('/shares') ? [] : record)
  const root = await mount(DocumentDetailView, {}, '/documents/doc-1')
  expect(root.textContent).toContain('Privado · Solo tú')
  expect(root.textContent).not.toContain('Editar metadatos')
  document.getElementById('document-detail-actions')!.click(); await flush()
  const edit = [...document.querySelectorAll<HTMLElement>('[role=menuitem]')].find(item => item.textContent?.includes('Editar metadatos'))!
  edit.click(); await flush()
  expect(document.querySelector('#document-title')).not.toBeNull()
  button('Cancelar').click(); await flush()
  expect(document.querySelector('[role=dialog]')).toBeNull()
  expect(document.activeElement?.id).toBe('document-detail-actions')
})
it('keeps access unknown on a failed check and retries without claiming the document is private', async () => {
  vi.mocked(get).mockResolvedValueOnce(record).mockRejectedValueOnce(new ApiError(500)).mockResolvedValueOnce([{}])
  const root = await mount(DocumentDetailView, {}, '/documents/doc-1')
  expect(root.textContent).toContain('Acceso controlado')
  expect(root.textContent).not.toContain('Privado · Solo tú')
  button('Reintentar consulta', root).click(); await flush()
  expect(root.textContent).toContain('Lo compartes con 1 miembro del hogar.')
})
it('disables detail downloads without an original and hides owner actions without grants', async () => {
  login(['documents.read'])
  vi.mocked(get).mockImplementation(async path => path.endsWith('/shares') ? [] : { ...record, hasFile: false })
  const root = await mount(DocumentDetailView, {}, '/documents/doc-1')
  expect(button('Descargar original', root).disabled).toBe(true)
  expect(root.querySelector('#document-detail-actions')).toBeNull()
  expect(root.textContent).toContain('Original no disponible')
})
it('discards pending document data when reading permission is removed', async () => {
  let resolve!: (value: Document) => void
  vi.mocked(get).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const root = await mount(DocumentDetailView, {}, '/documents/doc-1')
  login([]); await flush(); resolve(record); await flush()
  expect(root.textContent).not.toContain(record.title)
  expect(root.textContent).toContain('No tienes permiso')
  expect(get).toHaveBeenCalledTimes(1)
})
it.each([
  ['documents.write', 'Editar metadatos', 'Gestionar acceso'],
  ['documents.share', 'Gestionar acceso', 'Editar metadatos'],
])('limits detail actions to the owner grant %s', async (grant, visible, hidden) => {
  login(['documents.read', grant])
  vi.mocked(get).mockImplementation(async path => path.endsWith('/shares') ? [] : record)
  await mount(DocumentDetailView, {}, '/documents/doc-1')
  document.getElementById('document-detail-actions')!.click(); await flush()
  const menu = document.querySelector('[role=menu]')!
  expect(menu.textContent).toContain(visible)
  expect(menu.textContent).not.toContain(hidden)
})

it('restores library filters, page and view from direct URLs and browser history', async () => {
  const root = await mount(DocumentsView, {}, '/documents?q=manual&category=manuales&offset=50&view=list')
  expect((root.querySelector('#document-query') as HTMLInputElement).value).toBe('manual')
  expect(root.querySelector('#category-filter')?.textContent).toContain('Manuales')
  expect(root.querySelector('[role=tab][aria-selected=true]')?.textContent).toBe('Lista')
  expect(get).toHaveBeenLastCalledWith('/api/documents?q=manual&category=manuales&offset=50')
  input('document-query', 'contrato'); submit(root); await flush()
  expect(activeRouter.currentRoute.value.query).toEqual({ q: 'contrato', category: 'manuales', view: 'list' })
  activeRouter.back(); await flush()
  expect((root.querySelector('#document-query') as HTMLInputElement).value).toBe('manual')
  expect(get).toHaveBeenLastCalledWith('/api/documents?q=manual&category=manuales&offset=50')
  activeRouter.forward(); await flush()
  expect((root.querySelector('#document-query') as HTMLInputElement).value).toBe('contrato')
})
