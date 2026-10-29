<script setup lang="ts">
import { PasswordInput } from '@/components/ui/input'
import { ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ApiError, post } from '@/core/api'
import { returnToSection } from '@/core/navigation'
import { setAuthState, smtpEnabled } from '@/core/auth'
import { Button, buttonVariants } from '@/components/ui/button'
import AuthLayout from '@/components/AuthLayout.vue'
import BrandLogo from '@/components/BrandLogo.vue'

const code = ref(''), password = ref(''), confirmation = ref('')
const busy = ref(false), completed = ref(false), error = ref('')
const email = ref(''), requesting = ref(false), requested = ref(false), requestError = ref('')
const route = useRoute(), router = useRouter()
const useCode = ref(!smtpEnabled.value)
watch(() => route.hash, async hash => {
  if (!hash || hash === '#main-content') return
  code.value = hash.slice(1); useCode.value = true; error.value = ''; completed.value = false
  await router.replace('/forgot-password')
}, { immediate: true })
async function requestEmail() {
  if (requesting.value) return
  requesting.value = true; requestError.value = ''; requested.value = false
  try { await post('/api/auth/recovery/request', { email: email.value }); requested.value = true }
  catch (cause) { requestError.value = cause instanceof ApiError && cause.status === 429 ? 'Hay demasiados intentos. Espera unos minutos.' : 'No pudimos solicitar el correo. La recuperación local sigue disponible.' }
  finally { requesting.value = false }
}
async function submit() {
  if (busy.value) return
  error.value = ''
  if ([...password.value].length < 8 || [...password.value].length > 128) {
    error.value = 'Usa una contraseña de entre 8 y 128 caracteres.'
    return
  }
  if (password.value !== confirmation.value) { error.value = 'Las contraseñas no coinciden.'; return }
  busy.value = true
  try {
    await post('/api/auth/recover', { code: code.value, password: password.value })
    code.value = ''; completed.value = true
    setAuthState({ setupRequired: false, session: null, smtpEnabled: smtpEnabled.value })
  } catch (cause) {
    error.value = cause instanceof ApiError && cause.status === 410
      ? 'El código no está disponible: puede haber caducado, haberse usado o sido reemplazado. Pide uno nuevo.'
      : cause instanceof ApiError && cause.status === 429
        ? 'Hay demasiados intentos. Espera unos minutos antes de volver a intentarlo.'
        : cause instanceof ApiError && cause.status === 400
          ? 'Revisa el código y usa una contraseña de entre 8 y 128 caracteres.'
          : 'No pudimos recuperar el acceso. Comprueba la conexión y vuelve a intentarlo.'
  } finally { password.value = ''; confirmation.value = ''; busy.value = false }
}
</script>

<template>
  <AuthLayout>
    <section aria-labelledby="recovery-title" class="mx-auto w-full max-w-md">
      <div class="text-center">
        <RouterLink to="/login" aria-label="HomeAtlas, inicio de sesión" class="inline-flex rounded-lg outline-none focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring"><BrandLogo size="large" /></RouterLink>
        <h1 id="recovery-title" class="mt-8 text-[1.75rem] leading-tight font-extrabold tracking-tight sm:text-[2rem]">{{ completed ? 'Vuelve a tu hogar' : '¿Olvidaste tu contraseña?' }}</h1>
        <p class="mt-3 text-sm leading-6 text-muted-foreground sm:text-base">{{ completed ? 'Tu acceso está listo de nuevo.' : useCode ? 'Usa tu código para crear una nueva contraseña.' : 'Te ayudamos a volver a tu hogar.' }}</p>
      </div>

      <div v-if="completed" class="mt-8 space-y-6">
        <div role="status" class="rounded-xl bg-secondary p-5 text-sm leading-6"><h2 class="font-semibold text-heading">Contraseña actualizada</h2><p class="mt-2 text-muted-foreground">Se cerraron las sesiones anteriores de tu cuenta y se anularon las invitaciones pendientes que habías creado. Inicia sesión con tu nueva contraseña.</p></div>
        <RouterLink to="/login" replace :class="buttonVariants({ size: 'lg', class: 'w-full' })">Ir al inicio de sesión</RouterLink>
      </div>
      <template v-else>
        <form v-if="smtpEnabled && !useCode" class="mt-8 space-y-5" :aria-busy="requesting" @submit.prevent="requestEmail">
          <div class="space-y-2">
            <label for="recovery-email" class="form-label">Correo electrónico</label>
            <input id="recovery-email" v-model="email" type="email" inputmode="email" autocomplete="username" autocapitalize="none" :spellcheck="false" required maxlength="254" placeholder="tu@correo.com" class="form-input form-input-lg" :disabled="requesting" aria-describedby="recovery-email-help" />
            <p id="recovery-email-help" class="text-xs leading-5 text-muted-foreground">Usa el correo verificado de tu cuenta.</p>
          </div>
          <p v-if="requested" role="status" class="rounded-lg bg-secondary p-4 text-sm leading-6 text-muted-foreground">Si existe una cuenta activa con ese correo verificado, recibirás un enlace. Revisa también spam; si no llega, puedes usar tu código de recuperación.</p>
          <p v-if="requestError" role="alert" class="rounded-lg bg-danger-muted p-3 text-sm leading-6 text-destructive">{{ requestError }}</p>
          <Button type="submit" size="lg" class="w-full" :disabled="requesting">{{ requesting ? 'Solicitando enlace…' : 'Enviar enlace de recuperación' }}</Button>
        </form>

        <form v-else class="mt-8 space-y-5" :aria-busy="busy" @submit.prevent="submit">
          <p class="text-sm leading-6 text-muted-foreground">{{ smtpEnabled ? 'Introduce el código que recibiste por correo o solicita uno a quien administra tu servidor.' : 'Solicita un código a quien administra tu servidor. No necesitas recibir correos.' }} Caduca en 15 minutos y solo puede usarse una vez.</p>
          <fieldset :disabled="busy" class="space-y-5">
            <div class="space-y-2"><label for="recovery-code" class="form-label">Código de recuperación</label><PasswordInput id="recovery-code" v-model="code" name="code" autocomplete="off" autocapitalize="off" :spellcheck="false" maxlength="64" required class="form-input form-input-lg" aria-describedby="recovery-code-help" :disabled="busy" visibility-label="código de recuperación" /><p id="recovery-code-help" class="text-xs leading-5 text-muted-foreground">Es privado. No lo publiques ni lo compartas en una URL.</p></div>
            <div class="space-y-2"><label for="recovery-password" class="form-label">Nueva contraseña</label><PasswordInput id="recovery-password" v-model="password" name="password" autocomplete="new-password" minlength="8" maxlength="256" required class="form-input form-input-lg" aria-describedby="recovery-password-help" :disabled="busy" visibility-label="nueva contraseña" /><p id="recovery-password-help" class="text-xs leading-5 text-muted-foreground">Entre 8 y 128 caracteres. Puedes usar una frase larga.</p></div>
            <div class="space-y-2"><label for="recovery-confirm" class="form-label">Repite la nueva contraseña</label><PasswordInput id="recovery-confirm" v-model="confirmation" autocomplete="new-password" minlength="8" maxlength="256" required class="form-input form-input-lg" :disabled="busy" visibility-label="confirmación de nueva contraseña" /></div>
          </fieldset>
          <p v-if="error" role="alert" class="rounded-lg bg-danger-muted p-3 text-sm leading-6 text-destructive">{{ error }}</p>
          <Button type="submit" size="lg" :disabled="busy" class="w-full">{{ busy ? 'Actualizando contraseña…' : 'Actualizar contraseña' }}</Button>
        </form>

        <div v-if="smtpEnabled" class="mt-5 text-center"><Button type="button" variant="link" :disabled="busy || requesting" class="max-w-full whitespace-normal" @click="useCode = !useCode">{{ useCode ? 'Recibir un enlace por correo' : 'Ya tengo un código de recuperación' }}</Button></div>
        <div class="mt-7 border-t pt-5 text-center"><Button variant="ghost" class="text-primary" @click="returnToSection(router, '/login')"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="size-5"><path d="M19 12H5m6-6-6 6 6 6" /></svg>Volver al inicio de sesión</Button></div>
      </template>
    </section>
  </AuthLayout>
</template>
