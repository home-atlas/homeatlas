// @vitest-environment happy-dom
import { afterEach, expect, it } from 'vitest'
import { createApp, defineComponent, nextTick, ref } from 'vue'
import PasswordInput from './PasswordInput.vue'

const apps: { unmount: () => void }[] = []
afterEach(() => { apps.splice(0).forEach(app => app.unmount()); document.body.innerHTML = '' })

function mount() {
  const value = ref('test-only-secret'), confirmation = ref(''), disabled = ref(false), readonly = ref(false)
  let submissions = 0
  const root = document.createElement('div')
  const app = createApp(defineComponent({
    components: { PasswordInput },
    setup: () => ({ value, confirmation, disabled, readonly, submit: () => submissions++ }),
    template: `<form @submit.prevent="submit"><label for="secret">Contraseña</label>
      <PasswordInput id="secret" v-model="value" name="password" autocomplete="new-password" required minlength="8" maxlength="128" aria-describedby="help" :disabled="disabled" :readonly="readonly" class="form-input-lg" />
      <p id="help">Entre 8 y 128 caracteres</p>
      <PasswordInput id="confirmation" v-model="confirmation" visibility-label="confirmación de contraseña" />
    </form>`,
  }))
  document.body.append(root); app.mount(root); apps.push(app)
  return { root, value, disabled, readonly, submissions: () => submissions }
}

it('reveals only its own field without submitting or losing native field attributes', async () => {
  const { root, value, submissions } = mount()
  const input = root.querySelector<HTMLInputElement>('#secret')!
  const confirmation = root.querySelector<HTMLInputElement>('#confirmation')!
  const toggle = root.querySelector<HTMLButtonElement>('[aria-controls="secret"]')!
  expect(input.type).toBe('password')
  expect(input.labels?.[0]?.textContent).toBe('Contraseña')
  expect(input.autocomplete).toBe('new-password'); expect(input.name).toBe('password')
  expect(input.required).toBe(true); expect(input.minLength).toBe(8); expect(input.maxLength).toBe(128)
  expect(input.getAttribute('aria-describedby')).toBe('help')
  expect(input.classList.contains('form-input-lg')).toBe(true)
  expect(toggle.type).toBe('button'); expect(toggle.getAttribute('aria-pressed')).toBe('false')
  toggle.click(); await nextTick()
  expect(input.type).toBe('text'); expect(input.value).toBe('test-only-secret')
  expect(confirmation.type).toBe('password')
  expect(toggle.getAttribute('aria-label')).toBe('Ocultar contraseña')
  expect(toggle.getAttribute('aria-pressed')).toBe('true')
  expect(submissions()).toBe(0)
  input.value = 'updated-test-secret'; input.dispatchEvent(new Event('input', { bubbles: true })); await nextTick()
  expect(value.value).toBe('updated-test-secret')
  expect(new FormData(root.querySelector('form')!).get('password')).toBe('updated-test-secret')
  toggle.click(); await nextTick(); expect(input.type).toBe('password')
})

it('conceals cleared, disabled and readonly values and disables the corresponding toggle', async () => {
  const { root, value, disabled, readonly } = mount()
  const input = root.querySelector<HTMLInputElement>('#secret')!
  const toggle = root.querySelector<HTMLButtonElement>('[aria-controls="secret"]')!
  toggle.click(); await nextTick()
  value.value = ''; await nextTick(); expect(input.type).toBe('password')
  value.value = 'test-only-secret'; await nextTick(); toggle.click(); await nextTick()
  disabled.value = true; await nextTick()
  expect(input.type).toBe('password'); expect(input.disabled).toBe(true); expect(toggle.disabled).toBe(true)
  disabled.value = false; await nextTick(); toggle.click(); await nextTick()
  readonly.value = true; await nextTick()
  expect(input.type).toBe('password'); expect(input.readOnly).toBe(true); expect(toggle.disabled).toBe(true)
})
