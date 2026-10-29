<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, post } from '@/core/api'
import { setAuthState, type AuthState } from '@/core/auth'
import { Button } from '@/components/ui/button'
import { Input, PasswordInput } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import AppIcon from '@/components/AppIcon.vue'
import InstallationRecoveryCard from '@/components/InstallationRecoveryCard.vue'

const router = useRouter()
const busy = ref(false)
const error = ref('')
const mode = ref<'create' | 'recover'>('create'), recovering = ref(false), recovered = ref(false)
const confirmation = ref('')
const showPassword = ref(false), showConfirmation = ref(false)
function hidePasswords() { showPassword.value = false; showConfirmation.value = false }
watch(mode, hidePasswords)
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const timezones = [...new Set(['UTC', timezone, ...Intl.supportedValuesOf('timeZone')])].sort()
const form = reactive({ code: '', name: '', email: '', username: '', password: '', householdName: '', timezone })

async function submit() {
  if (busy.value) return
  hidePasswords()
  error.value = ''
  if ([...form.password].length < 8 || [...form.password].length > 128) {
    error.value = 'Usa una contraseña de entre 8 y 128 caracteres. Una frase larga es una buena opción.'
    return
  }
  if (form.password !== confirmation.value) { error.value = 'Las contraseñas no coinciden.'; return }
  busy.value = true
  try {
    const next = await post<AuthState>('/api/setup', form)
    form.code = ''; form.password = ''; confirmation.value = ''
    setAuthState(next)
    await router.replace('/')
  } catch (cause) {
    if (cause instanceof ApiError && cause.code === 'setup_completed') {
      form.code = ''; form.password = ''; confirmation.value = ''
      await router.replace('/login')
      return
    }
    error.value = cause instanceof ApiError && cause.code === 'invalid_setup_code'
      ? 'El código de alta no es correcto. Consulta el archivo privado setup-code de esta instalación.'
      : cause instanceof ApiError && cause.status === 429
      ? 'Hay demasiados intentos. Espera un momento antes de volver a intentarlo.'
      : cause instanceof ApiError && cause.status === 400
        ? 'Revisa los datos del hogar, el correo, la contraseña y la zona horaria.'
        : 'No pudimos completar la configuración. Comprueba la conexión y vuelve a intentarlo.'
  } finally { busy.value = false }
}
</script>

<template>
  <section aria-labelledby="setup-title" class="space-y-6">
    <div class="text-center"><p class="mb-3 text-sm font-medium text-primary">Bienvenido a HomeAtlas</p><h1 id="setup-title" class="text-3xl font-semibold tracking-tight">Un espacio para tu hogar</h1><p class="mt-3 text-sm leading-6 text-muted-foreground">Crea tu cuenta y organiza tu hogar en este servidor.</p></div>
    <div v-if="!recovered" class="flex flex-wrap gap-3" aria-label="Tipo de instalación"><Button :variant="mode === 'create' ? 'default' : 'outline'" :aria-pressed="mode === 'create'" :disabled="busy || recovering" @click="mode = 'create'">Crear un hogar nuevo</Button><Button :variant="mode === 'recover' ? 'default' : 'outline'" :aria-pressed="mode === 'recover'" :disabled="busy || recovering" @click="mode = 'recover'">Recuperar desde una copia</Button></div>
    <InstallationRecoveryCard v-if="mode === 'recover'" :disabled="busy" @busy="recovering = $event" @completed="recovered = true" />
    <Card v-else>
      <CardHeader><CardTitle>Alta inicial de tu hogar</CardTitle><CardDescription>Tu cuenta de propietario, sin pagos ni suscripciones.</CardDescription></CardHeader>
      <CardContent>
        <form class="space-y-5" :aria-busy="busy" @submit.prevent="submit">
          <fieldset :disabled="busy" class="space-y-4">
            <div class="space-y-2"><Label for="setup-code" class="form-label">Código de alta de la instalación</Label><PasswordInput id="setup-code" v-model="form.code" name="code" visibility-label="código de alta" :disabled="busy" autocomplete="off" autocapitalize="off" :spellcheck="false" maxlength="64" required /></div>
            <div class="grid gap-4 sm:grid-cols-2">
              <div class="space-y-2"><Label for="owner-name" class="form-label">Tu nombre</Label><Input id="owner-name" v-model="form.name" name="name" autocomplete="name" maxlength="80" required /></div>
              <div class="space-y-2"><Label for="household-name" class="form-label">Nombre del hogar</Label><Input id="household-name" v-model="form.householdName" name="householdName" placeholder="Nuestro hogar" maxlength="100" required /></div>
            </div>
            <div class="space-y-2"><Label for="setup-username" class="form-label">Nombre de usuario</Label><Input id="setup-username" v-model="form.username" autocomplete="username" autocapitalize="none" :spellcheck="false" minlength="3" maxlength="40" pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,39}" required /></div>
            <div class="space-y-2"><Label for="setup-email" class="form-label">Correo electrónico (opcional)</Label><Input id="setup-email" v-model="form.email" name="email" type="email" autocomplete="email" maxlength="254" /></div>
            <div class="space-y-2">
              <Label for="setup-password" class="form-label">Contraseña <span class="font-normal text-muted-foreground">(8–128 caracteres)</span></Label>
              <PasswordInput id="setup-password" v-model="form.password" v-model:revealed="showPassword" name="password" autocomplete="new-password" minlength="8" maxlength="256" required :disabled="busy" />
            </div>
            <div class="space-y-2">
              <Label for="setup-confirm" class="form-label">Repite la contraseña</Label>
              <PasswordInput id="setup-confirm" v-model="confirmation" v-model:revealed="showConfirmation" name="confirmation" autocomplete="new-password" minlength="8" maxlength="256" required :disabled="busy" visibility-label="confirmación de contraseña" />
            </div>
            <div class="space-y-2"><Label for="setup-timezone" class="form-label">Zona horaria del hogar</Label><Select v-model="form.timezone" name="timezone"><SelectTrigger id="setup-timezone"><SelectValue /></SelectTrigger><SelectContent><SelectItem v-for="zone in timezones" :key="zone" :value="zone">{{ zone }}</SelectItem></SelectContent></Select></div>
            <Collapsible>
              <CollapsibleTrigger as-child><Button type="button" variant="ghost" class="group -ml-3 px-3 text-muted-foreground">Ayuda para el alta<AppIcon name="chevron" class="size-4 group-data-[state=open]:rotate-180" /></Button></CollapsibleTrigger>
              <CollapsibleContent class="space-y-3 pt-2 text-sm leading-6 text-muted-foreground">
                <p><strong>Código de alta:</strong> está en el archivo privado <code>setup-code</code> de la carpeta de datos del servidor. Solo sirve para esta primera alta.</p>
                <p><strong>Usuario:</strong> entre 3 y 40 caracteres; letras sin acentos, números, puntos o guiones. Te permite entrar sin correo.</p>
                <p><strong>Correo:</strong> es opcional. Para recuperar el acceso por correo necesitas verificarlo y tener el envío habilitado.</p>
              </CollapsibleContent>
            </Collapsible>
          </fieldset>
          <Alert v-if="error" role="alert" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
          <Button type="submit" :disabled="busy" class="w-full">{{ busy ? 'Creando tu hogar…' : 'Crear mi hogar' }}</Button>
        </form>
      </CardContent>
    </Card>
  </section>
</template>
