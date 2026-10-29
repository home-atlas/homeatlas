// @vitest-environment happy-dom
import { expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { post } from '@/core/api'
import { setAuthState } from '@/core/auth'
import AssistedAccessCard from './AssistedAccessCard.vue'
vi.mock('@/core/api', async importOriginal => ({ ...await importOriginal<typeof import('@/core/api')>(), post: vi.fn() }))
async function flush() { await new Promise(resolve => setTimeout(resolve, 0)); await nextTick() }
it('guides the helper through safe defaults, sharing instructions, and a clean next enrollment', async () => {
  vi.mocked(post).mockResolvedValue({ url: 'https://example.test/activate-access#example-only' })
  const family = { members: [], canManage: true, roles: ['admin', 'member', 'restricted', 'guest'], capabilities: ['household.manage', 'documents.read', 'documents.write'] }
  const root = document.createElement('div'); document.body.append(root)
  const app = createApp(AssistedAccessCard, { family }); app.mount(root)
  try {
    root.querySelector('button')!.click(); await flush()
    const dialog = document.querySelector<HTMLElement>('[role=dialog]')!
    expect(dialog).not.toBeNull()
    expect(dialog.querySelector('#assisted-role')!.textContent).toContain('Restringido')
    expect(dialog.querySelector<HTMLInputElement>('#assisted-email')!.required).toBe(false)
    expect(dialog.querySelector('#assisted-customize')?.textContent).toContain('Consultar y descargar')
    expect(dialog.querySelector('[data-slot=checkbox]')).toBeNull()
    for (const [id, value] of [['assisted-name', 'María'], ['assisted-username', 'maria'], ['assisted-proof', 'example-test-proof']]) {
      const input = dialog.querySelector<HTMLInputElement>('#' + id)!
      input.value = value!; input.dispatchEvent(new Event('input', { bubbles: true }))
    }
    dialog.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
    expect(post).toHaveBeenCalledWith('/api/household/assisted-members', expect.objectContaining({ role: 'restricted', email: '', permissions: ['documents.read'] }))
    expect(dialog.textContent).toContain('No lo abras en tu equipo')
    expect(dialog.textContent).toContain('15 minutos')
    expect(dialog.textContent).toContain('Su cuenta y documentos se conservarán')
    expect(dialog.querySelector('#assisted-proof')).toBeNull()
    ;[...dialog.querySelectorAll('button')].find(b => b.textContent === 'Terminar')!.click(); await flush()
    root.querySelector('button')!.click(); await flush()
    expect(document.querySelector<HTMLInputElement>('#assisted-name')!.value).toBe('')
    expect(document.querySelector('#assisted-link')).toBeNull()
  } finally { app.unmount(); document.body.innerHTML = ''; vi.resetAllMocks(); setAuthState({ setupRequired: false, session: null }) }
})
it('selects an existing assisted member with shadcn controls and renews that account only', async () => {
  vi.mocked(post).mockResolvedValue({ url: 'https://example.test/activate-access#renew-test' })
  const family = { members: [{ id: 'maria', name: 'María', assisted: true, role: 'restricted', permissions: ['documents.read'] }], canManage: true, roles: ['admin', 'member', 'restricted'], capabilities: ['documents.read'] }
  const root = document.createElement('div'); document.body.append(root)
  const app = createApp(AssistedAccessCard, { family }); app.mount(root)
  async function select(id: string, label: string) {
    document.getElementById(id)!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); await flush()
    const option = [...document.querySelectorAll<HTMLElement>('[role=option]')].find(option => option.textContent?.trim() === label)!
    expect(option).toBeDefined(); option.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); await flush()
  }
  try {
    root.querySelector('button')!.click(); await flush()
    await select('assisted-mode', 'Ayudar a alguien que ya tiene cuenta')
    expect(document.querySelector('#assisted-name')).toBeNull()
    await select('assisted-target', 'María')
    const proof = document.querySelector<HTMLInputElement>('#assisted-proof')!
    proof.value = 'example-test-proof'; proof.dispatchEvent(new Event('input', { bubbles: true }))
    document.querySelector('[role=dialog] form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); await flush()
    expect(post).toHaveBeenCalledWith('/api/household/members/maria/enrollment', { password: 'example-test-proof' })
    expect(document.querySelector('#assisted-proof')).toBeNull()
  } finally { app.unmount(); document.body.innerHTML = ''; vi.resetAllMocks(); setAuthState({ setupRequired: false, session: null }) }
})
