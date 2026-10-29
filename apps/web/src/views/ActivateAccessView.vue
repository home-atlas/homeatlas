<script setup lang="ts">
import { returnToSection } from '@/core/navigation'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { nextTick, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { post } from '@/core/api'
import { logout, session } from '@/core/auth'
import { accessError, validPIN } from '@/core/alternative-access'
import AuthLayout from '@/components/AuthLayout.vue'
import BrandLogo from '@/components/BrandLogo.vue'
import AppIcon from '@/components/AppIcon.vue'
import PINField from '@/components/PINField.vue'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Input, PasswordInput } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button, buttonVariants } from '@/components/ui/button'
const route = useRoute(), router = useRouter()
const token = ref(''), pin = ref(''), confirmation = ref(''), deviceName = ref('Mi teléfono o computadora')
const person = ref<{ name: string; username: string }>(), loading = ref(false), busy = ref(false), completed = ref(false), error = ref('')
const step = ref<1 | 2>(1), pinInvalid = ref(false), confirmationInvalid = ref(false)
const pinField = ref<InstanceType<typeof PINField>>(), confirmationField = ref<InstanceType<typeof PINField>>()
const result = ref<HTMLElement>()
async function inspect() {
  if (loading.value) return
  loading.value = true; error.value = ''; person.value = undefined
  step.value = 1; pin.value = ''; confirmation.value = ''; pinInvalid.value = false; confirmationInvalid.value = false
  try { person.value = await post('/api/assisted/inspect', { token: token.value }) }
  catch (cause) { error.value = accessError(cause) }
  finally { loading.value = false }
}
watch(() => route.hash, async hash => {
  if (!hash || hash === '#main-content') return
  token.value = hash.slice(1)
  await router.replace('/activate-access')
  await inspect()
}, { immediate: true })
async function signOut() {
  busy.value = true; error.value = ''
  try { await logout() } catch (cause) { error.value = accessError(cause) }
  finally { busy.value = false }
}
async function back() {
  step.value = 1; error.value = ''; confirmation.value = ''; confirmationInvalid.value = false
  await nextTick(); pinField.value?.focus()
}
async function activate() {
  if (busy.value) return
  error.value = ''; pinInvalid.value = false; confirmationInvalid.value = false
  if (!validPIN(pin.value)) {
    step.value = 1; pinInvalid.value = true; error.value = 'Escribe entre 6 y 12 números para tu PIN.'
    await nextTick(); pinField.value?.focus(); return
  }
  if (step.value === 1) {
    step.value = 2; await nextTick(); confirmationField.value?.focus(); return
  }
  if (pin.value !== confirmation.value) {
    confirmationInvalid.value = true; error.value = 'Los números no coinciden. Repite el PIN o vuelve atrás para elegir otro.'
    confirmationField.value?.focus(); return
  }
  busy.value = true
  try {
    await post('/api/assisted/complete', { token: token.value, pin: pin.value, deviceName: deviceName.value.trim() || 'Mi teléfono o computadora' })
    token.value = ''; completed.value = true
    await nextTick(); result.value?.focus()
  } catch (cause) {
    error.value = accessError(cause)
    step.value = 1
    await nextTick(); pinField.value?.focus()
  } finally { pin.value = ''; confirmation.value = ''; busy.value = false }
}
</script>
<template>
  <AuthLayout><section aria-labelledby="activation-title" class="mx-auto w-full max-w-md">
    <div class="text-center">
      <BrandLogo size="large" class="justify-center" />
      <h1 id="activation-title" class="mt-8 text-[1.75rem] font-extrabold tracking-tight sm:text-[2rem]">{{ completed ? 'Ya puedes entrar' : person ? `Hola, ${person.name}` : 'Prepara tu acceso con ayuda' }}</h1>
      <p v-if="!completed" class="mt-3 text-base leading-7 text-muted-foreground">{{ person ? 'Entrarás con un PIN: un número que eliges tú. No necesitas correo ni contraseña.' : 'Una persona de tu hogar puede ayudarte a empezar.' }}</p>
    </div>
    <Alert v-if="loading" role="status" class="mt-8 text-center text-base text-muted-foreground"><AlertDescription class="text-inherit leading-7">Estamos comprobando tu enlace…</AlertDescription></Alert>
    <div v-else-if="completed" class="mt-8 space-y-6">
      <h2 ref="result" tabindex="-1" class="rounded-lg text-xl font-semibold focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-ring">Tu PIN está listo</h2>
      <p class="text-base leading-7">La próxima vez, abre HomeAtlas en este mismo teléfono o computadora y escribe tu PIN.</p>
      <RouterLink to="/login" replace :class="buttonVariants({ size: 'lg', class: 'w-full text-base' })">Entrar con mi PIN</RouterLink>
      <div class="rounded-xl bg-secondary p-5 text-base leading-7 text-muted-foreground"><h3 class="font-semibold">Si necesitas ayuda</h3><p class="mt-2">Si olvidas tu PIN o pasan 30 días, pide a quien administra tu hogar un nuevo enlace. No necesitas crear otra cuenta.</p></div>
      <p class="text-base leading-7 text-muted-foreground">Si antes usabas otro equipo, ya no podrá entrar con el PIN anterior.</p>
    </div>
    <div v-else-if="person && session" class="mt-8 space-y-5">
      <h2 class="text-xl font-semibold">Primero, cierra la otra cuenta</h2>
      <p class="rounded-xl bg-secondary p-5 text-base leading-7">Está abierta la cuenta de <strong>{{ session.user.name }}</strong>. Ciérrala para preparar el acceso de <strong>{{ person.name }}</strong>. Sus documentos se conservarán.</p>
      <Button size="lg" :disabled="busy" class="w-full text-base" @click="signOut">{{ busy ? 'Cerrando cuenta…' : 'Cerrar esa cuenta y continuar' }}</Button>
    </div>
    <form v-else-if="person" class="mt-7 space-y-5" :aria-busy="busy" @submit.prevent="activate">
      <p class="text-base font-semibold text-primary" aria-live="polite" aria-atomic="true">Paso {{ step }} de 2 · {{ step === 1 ? 'Elige tu PIN' : 'Confirma tu PIN' }}</p>
      <fieldset :disabled="busy" class="space-y-5">
        <template v-if="step === 1">
          <p class="rounded-xl bg-secondary p-4 text-base leading-7">Usa tu teléfono o computadora personal. El PIN funcionará solo aquí, en este mismo navegador.</p>
          <PINField id="activation-pin" ref="pinField" v-model="pin" label="Elige tu PIN" help="Entre 6 y 12 números, sin letras." autocomplete="new-password" :invalid="pinInvalid" />
          <Collapsible class="rounded-xl border p-4"><CollapsibleTrigger as-child><Button type="button" variant="ghost" class="w-full justify-between px-0 text-left text-base whitespace-normal">Nombre de este equipo (opcional)<AppIcon name="chevron" class="size-5" /></Button></CollapsibleTrigger><CollapsibleContent><div class="mt-3 space-y-2"><Label for="activation-device" class="form-label">¿Cómo reconoces este equipo?</Label><Input id="activation-device" v-model="deviceName" maxlength="80" class="form-input form-input-lg" /><p class="text-base leading-7 text-muted-foreground">Por ejemplo, «Teléfono de María». Puedes dejar el nombre que aparece.</p></div></CollapsibleContent></Collapsible>
        </template>
        <template v-else>
          <p class="text-base leading-7">Escribe de nuevo el número que acabas de elegir para comprobar que es el que quieres.</p>
          <PINField id="activation-confirm" ref="confirmationField" v-model="confirmation" label="Repite tu PIN" help="El mismo número del paso anterior." autocomplete="new-password" :invalid="confirmationInvalid" />
        </template>
        <Alert v-if="error" role="alert" class="rounded-lg bg-danger-muted p-4 text-base leading-7 text-destructive" variant="destructive"><AlertDescription class="text-inherit leading-7">{{ error }}</AlertDescription></Alert>
        <p v-if="step === 2" class="rounded-xl bg-secondary p-4 text-base leading-7">Al guardar, tus otros equipos con PIN dejarán de funcionar. Tu cuenta y tus documentos se conservarán.</p>
        <Button type="submit" size="lg" class="w-full text-base">{{ busy ? 'Preparando tu acceso…' : step === 1 ? 'Continuar' : 'Guardar mi PIN' }}</Button>
        <Button v-if="step === 2" type="button" variant="outline" size="lg" class="w-full text-base" @click="back">Volver y cambiar mi PIN</Button>
      </fieldset>
    </form>
    <form v-else class="mt-8 space-y-5" :aria-busy="loading" @submit.prevent="inspect">
      <h2 class="text-xl font-semibold">Abre el enlace que te compartieron</h2>
      <ol class="list-decimal space-y-3 pl-6 text-base leading-7"><li>Pide a quien administra tu hogar que prepare tu acceso desde <strong>Mi familia</strong>.</li><li>Abre su enlace en tu teléfono o computadora personal.</li><li>Aquí elegirás un PIN para entrar.</li></ol>
      <p class="text-base leading-7 text-muted-foreground">El enlace dura 15 minutos. Si ya no funciona, pide uno nuevo para tu misma cuenta.</p>
      <Alert v-if="error" role="alert" class="rounded-lg bg-danger-muted p-4 text-base leading-7 text-destructive" variant="destructive"><AlertDescription class="text-inherit leading-7">{{ error }}</AlertDescription></Alert>
      <Collapsible class="rounded-xl border p-4"><CollapsibleTrigger as-child><Button type="button" variant="ghost" class="w-full justify-between px-0 text-left text-base whitespace-normal">Tengo un código en lugar de un enlace<AppIcon name="chevron" class="size-5" /></Button></CollapsibleTrigger><CollapsibleContent><div class="mt-4 space-y-4"><Label for="activation-token" class="form-label">Código de acceso que te compartieron</Label><PasswordInput id="activation-token" v-model="token" autocomplete="off" maxlength="64" required class="form-input form-input-lg" :disabled="busy" visibility-label="código de acceso" /><Button type="submit" size="lg" class="w-full">Comprobar código</Button></div></CollapsibleContent></Collapsible>
    </form>
    <Alert v-if="error && person && session" role="alert" class="mt-5 rounded-lg bg-danger-muted p-4 text-base leading-7 text-destructive" variant="destructive"><AlertDescription class="text-inherit leading-7">{{ error }}</AlertDescription></Alert>
    <div v-if="!completed" class="mt-7 border-t pt-5 text-center"><Button variant="ghost" class="text-base text-primary" @click="returnToSection(router, '/login')">Volver al inicio de sesión</Button></div>
  </section></AuthLayout>
</template>
