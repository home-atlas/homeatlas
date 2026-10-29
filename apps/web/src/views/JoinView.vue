<script setup lang="ts">
import { Alert, AlertDescription } from '@/components/ui/alert'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { Input, PasswordInput } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { post } from '@/core/api'
import { logout, session, setAuthState, type AuthState } from '@/core/auth'
import { householdError, permissionLabel, roleLabel } from '@/core/household'
interface InvitationPreview { email: string; householdName: string; role: string; permissions: string[]; expiresAt: string; needsAccount: boolean }
const route = useRoute(), router = useRouter()
const token = ref(''), invitation = ref<InvitationPreview>(), loading = ref(false), busy = ref(false), error = ref(''), name = ref(''), password = ref(''), confirmation = ref('')
const mismatch = computed(() => session.value && invitation.value && session.value.user.email !== invitation.value.email)
let revision = 0
watch(() => route.hash, async hash => {
  if (!hash) { if (!token.value) error.value = 'Abre el enlace completo que te compartió quien administra el hogar.'; return }
  const current = ++revision
  token.value = hash.slice(1); loading.value = true; error.value = ''; invitation.value = undefined
  // Keep the bearer token only in memory, never in request URLs or browser storage.
  await router.replace('/join')
  try { const result = await post<InvitationPreview>('/api/invitations/inspect', { token: token.value }); if (current === revision) invitation.value = result }
  catch (err) { if (current === revision) error.value = householdError(err) }
  finally { if (current === revision) loading.value = false }
}, { immediate: true })
async function changeAccount() {
  if (busy.value) return; busy.value = true; error.value = ''
  try { await logout() } catch (err) { error.value = householdError(err) } finally { busy.value = false }
}
async function accept() {
  if (!invitation.value || busy.value || mismatch.value) return
  error.value = ''
  if (invitation.value.needsAccount && ([...password.value].length < 8 || [...password.value].length > 128)) { error.value = 'Usa una contraseña de entre 8 y 128 caracteres.'; return }
  if (invitation.value.needsAccount && password.value !== confirmation.value) { error.value = 'Las contraseñas no coinciden.'; return }
  busy.value = true
  try {
    const result = await post<AuthState>('/api/invitations/accept', { token: token.value, name: name.value, password: password.value })
    password.value = ''; confirmation.value = ''; token.value = ''; setAuthState(result); await router.replace('/family')
  } catch (err) { error.value = householdError(err) } finally { busy.value = false }
}
</script>
<template>
  <section aria-labelledby="join-title" class="space-y-6"><div><p class="mb-3 text-sm text-primary">Las personas que hacen hogar</p><h1 id="join-title" class="text-3xl font-semibold tracking-tight">Invitación al hogar</h1></div><p v-if="loading" aria-live="polite" class="py-8 text-sm text-muted-foreground">Comprobando la invitación…</p><Alert v-if="error" role="alert" class="rounded-xl border border-destructive/30 p-4 text-sm leading-6 text-destructive" variant="destructive"><AlertDescription class="text-inherit leading-7">{{ error }}</AlertDescription></Alert><Card v-if="invitation"><CardHeader><CardTitle class="text-xl">Únete a {{ invitation.householdName }}</CardTitle></CardHeader><CardContent class="space-y-6"><div class="rounded-xl bg-secondary/50 p-4 text-sm leading-7"><p class="break-all font-medium">{{ invitation.email }}</p><p>{{ roleLabel(invitation.role) }}</p><ul class="mt-3 space-y-1 text-xs text-muted-foreground"><li v-for="permission in invitation.permissions" :key="permission">{{ permissionLabel(permission) }}</li></ul><p class="mt-3 text-xs text-muted-foreground">Caduca {{ new Date(invitation.expiresAt).toLocaleString('es') }}</p></div><template v-if="mismatch"><p class="text-sm leading-6 text-muted-foreground">Tienes una sesión abierta con otra cuenta. Ciérrala aquí para aceptar con la cuenta invitada.</p><Button :disabled="busy" variant="outline" @click="changeAccount">Cerrar la sesión actual</Button></template><form v-else class="space-y-5" :aria-busy="busy" @submit.prevent="accept"><fieldset class="space-y-5" :disabled="busy"><div v-if="invitation.needsAccount" class="space-y-2"><Label for="join-name" class="form-label">Tu nombre</Label><Input id="join-name" v-model="name" class="form-input" maxlength="80" autocomplete="name" required /></div><template v-if="!session"><div class="space-y-2"><Label for="join-password" class="form-label">{{ invitation.needsAccount ? 'Crea tu contraseña' : 'Contraseña de tu cuenta existente' }}</Label><PasswordInput id="join-password" v-model="password" class="form-input" :autocomplete="invitation.needsAccount ? 'new-password' : 'current-password'" :minlength="invitation.needsAccount ? 8 : 1" maxlength="128" required :disabled="busy" /><p v-if="invitation.needsAccount" class="text-xs leading-5 text-muted-foreground">Entre 8 y 128 caracteres. Tu cuenta quedará vinculada al correo invitado.</p></div><div v-if="invitation.needsAccount" class="space-y-2"><Label for="join-confirmation" class="form-label">Repite la contraseña</Label><PasswordInput id="join-confirmation" v-model="confirmation" class="form-input" autocomplete="new-password" minlength="8" maxlength="128" required :disabled="busy" visibility-label="confirmación de contraseña" /></div></template><p v-else class="text-sm text-muted-foreground">Aceptarás con tu cuenta actual: {{ session.user.name }}.</p><p class="text-xs leading-6 text-muted-foreground">Self-hosted es gratuito. Unirte no requiere pagos. Solo verás tus documentos y los que compartan expresamente contigo.</p><Button class="w-full" type="submit" :disabled="busy">{{ busy ? 'Uniéndote…' : 'Aceptar invitación' }}</Button></fieldset></form></CardContent></Card><RouterLink v-if="!invitation && !loading" to="/login" class="inline-block text-sm text-primary underline">Ir al inicio de sesión</RouterLink></section>
</template>
