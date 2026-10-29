// @vitest-environment happy-dom
import { afterEach, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import GrantFields from './GrantFields.vue'
const apps: { unmount: () => void }[] = []
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
async function mount(capabilities = ['documents.read', 'documents.write', 'documents.share'], roles = ['member', 'restricted']) {
  const role = ref('member'), permissions = ref<string[]>([]), root = document.createElement('div')
  document.body.append(root)
  const app = createApp({ setup: () => () => h(GrantFields, { prefix: 'grant', roles, capabilities, role: role.value, permissions: permissions.value, 'onUpdate:role': value => { role.value = value }, 'onUpdate:permissions': value => { permissions.value = value } }) })
  apps.push(app); app.mount(root); await flush()
  return { role, permissions, root }
}
async function customize() { document.getElementById('grant-customize')!.click(); await flush() }
function button(name: string, scope: ParentNode = document) { return [...scope.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent?.includes(name))! }
function search(value: string) { const input = document.getElementById('grant-search') as HTMLInputElement; input.value = value; input.dispatchEvent(new Event('input', { bubbles: true })) }
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); document.body.innerHTML = '' })
it('starts with a compact summary and reveals the permission help only when requested', async () => {
  const { root, permissions } = await mount()
  expect(root.textContent).toContain('Invitar no da acceso a todos los documentos')
  expect(root.textContent).toContain('Sin permisos de módulos')
  expect(root.querySelector('[data-slot=checkbox]')).toBeNull()
  expect(root.textContent).not.toContain('No permite editar documentos recibidos')
  await customize()
  expect(document.querySelectorAll('[data-slot=checkbox]')).toHaveLength(3)
  document.querySelector<HTMLButtonElement>('[aria-label="Información: Subir documentos y editar los propios"]')!.click(); await flush()
  expect(root.textContent).toContain('No permite editar documentos recibidos')
  expect(permissions.value).toEqual([])
})
it('uses shadcn checkboxes and preserves read/write dependencies through label clicks', async () => {
  const { permissions, root } = await mount(); await customize()
  root.querySelector<HTMLElement>('label[for="grant-documents.write"]')!.click(); await flush()
  expect(permissions.value).toEqual(['documents.write', 'documents.read'])
  expect(document.getElementById('grant-documents.read')?.getAttribute('aria-checked')).toBe('true')
  await customize()
  expect(document.querySelector('[data-slot=checkbox]')).toBeNull()
  expect(document.getElementById('grant-customize')?.textContent).toContain('Subir y editar los propios')
  await customize()
  root.querySelector<HTMLElement>('label[for="grant-documents.read"]')!.click(); await flush()
  expect(permissions.value).toEqual([])
})
it('changes roles with the shadcn selector and removes forbidden write permissions', async () => {
  const { role, permissions } = await mount(); await customize()
  document.getElementById('grant-documents.write')!.click(); await flush()
  const trigger = document.getElementById('grant-role')!
  trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); await flush()
  const option = [...document.querySelectorAll<HTMLElement>('[role=option]')].find(item => item.textContent?.trim() === 'Restringido')!
  expect(option).toBeDefined(); option.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); await flush()
  expect(role.value).toBe('restricted'); expect(permissions.value).toEqual(['documents.read'])
  expect(document.getElementById('grant-documents.write')).toBeNull()
  expect(document.activeElement).toBe(trigger)
})
it('compares only roles offered by the server without submitting or changing permissions', async () => {
  const { root, permissions, role } = await mount(['documents.read'], ['admin', 'member', 'restricted', 'guest'])
  const trigger = button('Sobre los roles y la privacidad', root)
  expect(trigger.type).toBe('button'); expect(trigger.getAttribute('aria-expanded')).toBe('false')
  trigger.click(); await flush()
  expect(trigger.getAttribute('aria-expanded')).toBe('true')
  expect([...root.querySelectorAll('dt')].map(item => item.textContent)).toEqual(['Administrador', 'Miembro', 'Restringido', 'Invitado'])
  expect(root.textContent).toContain('no al propietario ni a otros administradores')
  expect(root.textContent).toContain('No se asigna mediante una invitación')
  expect(root.textContent).toContain('Solo el propietario puede nombrar administradores')
  role.value = 'guest'; await flush()
  expect(document.getElementById('grant-role-description')?.textContent).toContain('no caduca automáticamente')
  expect(permissions.value).toEqual([])
})
it('handles 50 permissions by module and preserves hidden selections through search and role changes', async () => {
  const capabilities = Array.from({ length: 10 }, (_, i) => ['read', 'write', 'share', 'delete', 'export'].map(action => `module-${i}.${action}`)).flat()
  const { root, role, permissions } = await mount(capabilities)
  permissions.value = ['module-0.read']; await flush()
  expect(root.querySelectorAll('[data-slot=checkbox]')).toHaveLength(0)
  expect(document.getElementById('grant-customize')?.textContent).toContain('1 permiso en 1 módulo')
  await customize()
  expect(root.querySelectorAll('[data-slot=checkbox]')).toHaveLength(0)
  expect(root.querySelectorAll('[role=region] > [data-slot=collapsible]')).toHaveLength(10)
  search('module-7.write'); await flush()
  expect(root.querySelectorAll('[data-slot=checkbox]')).toHaveLength(1)
  document.getElementById('grant-module-7.write')!.click(); await flush()
  expect(permissions.value).toEqual(['module-0.read', 'module-7.write', 'module-7.read'])
  search('sin coincidencias'); await flush()
  expect(root.textContent).toContain('No hay coincidencias')
  expect(permissions.value).toHaveLength(3)
  search(''); await flush()
  expect(root.querySelectorAll('[data-slot=checkbox]')).toHaveLength(0)
  role.value = 'restricted'; await flush()
  expect(permissions.value).toEqual(['module-0.read', 'module-7.read'])
  await customize()
  expect(root.querySelector('[data-slot=checkbox]')).toBeNull()
  expect(document.getElementById('grant-customize')?.textContent).toContain('2 permisos en 2 módulos')
})
it('keeps the summary short even with 50 selected permissions in the same module', async () => {
  const capabilities = Array.from({ length: 50 }, (_, i) => `documents.action-${i}`)
  const { root, permissions } = await mount(capabilities)
  permissions.value = capabilities; await flush()
  const summary = document.getElementById('grant-customize')!
  expect(summary.textContent).toContain('50 permisos en 1 módulo')
  expect(summary.textContent).not.toContain('documents.action-')
  expect(root.querySelector('[data-slot=checkbox]')).toBeNull()
})
it('retains existing revoke-only capabilities without allowing them to be granted again', async () => {
  const { permissions } = await mount(['documents.read'])
  permissions.value = ['documents.read', 'documents.write']; await customize()
  const checkbox = document.getElementById('grant-documents.write')!
  expect(checkbox.getAttribute('aria-checked')).toBe('true')
  checkbox.click(); await flush()
  expect(permissions.value).toEqual(['documents.read'])
  expect(document.getElementById('grant-documents.write')).toBeNull()
})
