<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ApiError, get, post } from '@/core/api'
import { safeReturnTo, setAuthState, type AuthState } from '@/core/auth'
import { Button } from '@/components/ui/button'
import { PasswordInput } from '@/components/ui/input'
import { validPIN, type PINState } from '@/core/alternative-access'
import PINField from '@/components/PINField.vue'
import AuthLayout from '@/components/AuthLayout.vue'
import BrandLogo from '@/components/BrandLogo.vue'
import AppIcon from '@/components/AppIcon.vue'

const route = useRoute()
const router = useRouter()
const identifier = ref('')
const identifierField = ref<HTMLInputElement>()
const modeChosen = ref(false)
let mounted = true
onUnmounted(() => { mounted = false })
const pin = ref(''), mode = ref<'password' | 'pin'>('password'), device = ref<PINState>({ available: false })
onMounted(async () => {
  try { device.value = await get<PINState>('/api/auth/pin'); if (mounted && route.path === '/login' && device.value.available && !modeChosen.value) await router.replace({ query: { ...route.query, method: 'pin' } }) } catch { /* Password login remains available when device lookup fails. */ }
})
async function chooseMode(next: 'password' | 'pin') {
  if (busy.value) return
  modeChosen.value = true
  const target = { query: { ...route.query, method: next } }
  const back = router.options.history.state.back
  if (next === 'password' && typeof back === 'string' && router.resolve(back).path === '/login' && router.resolve(back).query.method !== 'pin') router.back()
  else if (next === 'password') await router.replace(target)
  else await router.push(target)
}
const password = ref('')
const showPassword = ref(false)
const busy = ref(false)
const error = ref('')
const pinField = ref<InstanceType<typeof PINField>>()
watch(() => route.query.method, async method => {
  if (route.path !== '/login') return
  mode.value = method === 'pin' ? 'pin' : 'password'
  if (method === 'pin' || method === 'password') modeChosen.value = true
  pin.value = ''; password.value = ''; error.value = ''; showPassword.value = false
  await nextTick()
  if (!mounted || route.path !== '/login') return
  if (mode.value === 'password') identifierField.value?.focus()
  else pinField.value?.focus()
}, { immediate: true })

async function submit() {
  if (busy.value) return
  error.value = ''
  if (mode.value === 'pin' && !validPIN(pin.value)) { error.value = 'Escribe tu PIN completo: entre 6 y 12 números.'; pinField.value?.focus(); return }
  busy.value = true
  try {
    const next = await post<AuthState>(mode.value === 'pin' ? '/api/auth/pin/login' : '/api/auth/login', mode.value === 'pin' ? { pin: pin.value } : { identifier: identifier.value, password: password.value })
    password.value = ''
    setAuthState(next)
    await router.replace(safeReturnTo(route.query.redirect))
  } catch (cause) {
    password.value = ''
    error.value = cause instanceof ApiError && cause.status === 401 ? mode.value === 'pin' ? 'No pudimos entrar con ese PIN. Comprueba los números. Si no lo recuerdas, pide ayuda antes de volver a intentarlo.' : 'El usuario, correo o contraseña no son correctos.'
      : cause instanceof ApiError && cause.status === 429
        ? `Hay demasiados intentos. Vuelve a intentarlo ${cause.retryAfter >= 60 ? 'en unos minutos' : 'en un momento'}.`
        : 'No pudimos iniciar sesión. Comprueba la conexión y vuelve a intentarlo.'
    if (mode.value === 'pin' && cause instanceof ApiError && cause.status === 401) {
      try {
        device.value = await get<PINState>('/api/auth/pin')
        if (!device.value.available) error.value = 'El acceso con PIN ya no está disponible en este equipo. Puede haber caducado o bloqueado. Pide ayuda a quien administra tu hogar o entra con tu contraseña, si la tienes.'
      } catch { /* Keep the sign-in error and the password alternative available. */ }
    }
  } finally { busy.value = false; showPassword.value = false; pin.value = ''; await nextTick(); if (error.value && mode.value === 'pin') pinField.value?.focus() }
}
</script>

<template>
  <AuthLayout>
      <section aria-labelledby="login-title" class="mx-auto w-full max-w-md">
        <Button v-if="mode === 'pin'" type="button" variant="ghost" class="mb-4 -ml-3 px-3 text-primary" :disabled="busy" @click="chooseMode('password')"><AppIcon name="arrow" class="size-5 rotate-180" />Volver al inicio de sesión</Button>
        <div class="text-center">
          <RouterLink to="/" aria-label="HomeAtlas, inicio" class="inline-flex rounded-lg outline-none focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring"><BrandLogo size="large" /></RouterLink>
          <h1 id="login-title" class="mt-8 text-[1.75rem] leading-tight font-extrabold tracking-tight sm:text-[2rem]">{{ mode === 'pin' ? 'Entra con tu PIN' : 'Bienvenido de nuevo' }}</h1>
          <p class="mt-3 text-sm leading-6 text-muted-foreground sm:text-base">{{ mode === 'pin' && device.available ? `Hola, ${device.name}. Escribe tu PIN para entrar a tu hogar.` : mode === 'pin' ? 'Prepara el acceso con PIN en este equipo.' : 'Inicia sesión y vuelve a sentirte en casa.' }}</p>
        </div>

        <form v-if="mode === 'password' || device.available" class="mt-8" :aria-busy="busy" @submit.prevent="submit">
          <fieldset v-if="mode === 'password'" :disabled="busy" class="space-y-5">
            <div class="space-y-2">
              <label for="login-email" class="form-label">Usuario o correo electrónico</label>
              <div class="relative"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="pointer-events-none absolute top-1/2 left-4 size-5 -translate-y-1/2 text-muted-foreground"><circle cx="12" cy="8" r="4" /><path d="M4 21v-2a8 8 0 0 1 16 0v2" /></svg><input id="login-email" ref="identifierField" v-model="identifier" name="username" type="text" autocomplete="username" autocapitalize="none" :spellcheck="false" maxlength="254" placeholder="Tu usuario o correo" required class="form-input form-input-lg pl-12" /></div>
            </div>
            <div class="space-y-2">
              <label for="login-password" class="form-label">Contraseña</label>
              <div class="relative"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="pointer-events-none absolute top-1/2 left-4 z-10 size-5 -translate-y-1/2 text-muted-foreground"><rect x="4" y="10" width="16" height="11" rx="2" /><path d="M8 10V6a4 4 0 0 1 8 0v4M12 14v3" /></svg><PasswordInput id="login-password" v-model="password" v-model:revealed="showPassword" name="password" autocomplete="current-password" maxlength="256" placeholder="Tu contraseña" required :disabled="busy" class="form-input-lg pl-12" /></div>
            </div>
          </fieldset>
          <PINField v-else id="login-pin" ref="pinField" v-model="pin" label="Tu PIN" help="Entre 6 y 12 números." :disabled="busy" :invalid="!!error" />
          <p v-if="mode === 'pin'" class="mt-4 text-base leading-7 text-muted-foreground">Toca «Mostrar PIN» si necesitas comprobar lo que escribiste.</p>
          <div v-if="mode === 'password'" class="mt-2 flex justify-end"><RouterLink to="/forgot-password" class="inline-flex min-h-11 items-center rounded-lg text-sm font-medium text-primary outline-none hover:text-primary-hover hover:underline focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">¿Olvidaste tu contraseña?</RouterLink></div>
          <Button type="submit" size="lg" :disabled="busy" class="mt-5 w-full text-base">{{ busy ? 'Iniciando sesión…' : mode === 'pin' ? 'Entrar con mi PIN' : 'Iniciar sesión' }}<svg v-if="!busy" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="ml-1 size-5"><path d="M5 12h14m-6-6 6 6-6 6" /></svg></Button>
        </form>
        <div v-else class="mt-8 space-y-5 rounded-xl bg-secondary p-5 text-base leading-7">
          <h2 class="text-xl font-semibold">Primero, prepara este equipo</h2>
          <p>Si tu familia preparó tu cuenta, abre aquí el enlace que te compartieron para elegir tu PIN.</p>
          <p>Si ya entrabas solo con PIN, pide un nuevo enlace para tu misma cuenta. Tus documentos se conservarán.</p><p>Si tienes contraseña, entra con ella y activa el PIN desde <strong>Configuración → Mi acceso</strong>.</p>
        </div>
        <p v-if="error" role="alert" class="mt-4 rounded-lg bg-danger-muted p-4 text-base leading-7 text-destructive">{{ error }}</p>
        <details v-if="mode === 'pin' && device.available" class="mt-5 rounded-xl border p-4 text-base leading-7">
          <summary class="min-h-11 cursor-pointer py-2 font-semibold text-heading outline-none focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-ring">¿Olvidaste tu PIN o necesitas ayuda?</summary>
          <div class="mt-3 space-y-3 text-muted-foreground"><p>No sigas probando si no lo recuerdas: cinco errores bloquean este acceso.</p><p>Si tu familia preparó tu cuenta, pide a quien administra tu hogar que vaya a <strong>Mi familia → Preparar acceso con ayuda</strong> y elija <strong>Ayudar a alguien que ya tiene cuenta</strong>.</p><p>Abre el nuevo enlace en este equipo para elegir otro PIN. Tu cuenta y tus documentos se conservarán.</p><p>El acceso dura 30 días y solo funciona en este mismo navegador. Si tienes contraseña, también puedes usarla y preparar otro PIN en Configuración → Mi acceso.</p></div>
        </details>
        <div v-if="mode === 'password'" class="mt-5"><Button type="button" variant="outline" size="lg" class="w-full text-base" :disabled="busy" @click="chooseMode('pin')">Entrar con PIN</Button></div>
        <div class="mt-7 border-t pt-6 text-center"><p class="text-sm font-medium text-heading">¿Aún no tienes acceso?</p><p class="mx-auto mt-2 max-w-xs text-sm leading-6 text-muted-foreground">Pide ayuda a quien administra tu hogar para preparar tu acceso.</p></div>
      </section>
  </AuthLayout>
</template>
