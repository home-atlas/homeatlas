<script setup lang="ts">
import { Input, PasswordInput } from '@/components/ui/input'
import { onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { get, patch, post, remove } from '@/core/api'
import { refreshSession, session } from '@/core/auth'
import { accountContext } from '@/core/account'
import { accessError, validPIN, type AccessSettings } from '@/core/alternative-access'
import { formatHouseholdDate } from '@/core/datetime'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import ActionDialog from '@/components/ActionDialog.vue'
import PINField from '@/components/PINField.vue'
const router = useRouter(), settings = ref<AccessSettings>(), username = ref(''), password = ref(''), pinPassword = ref(''), pin = ref(''), confirmation = ref(''), deviceName = ref('Mi teléfono o computadora')
const busy = ref(false), loading = ref(false), error = ref(''), notice = ref(''), selected = ref<AccessSettings['devices'][number]>()
let active = true
const current = (context: string) => active && context === accountContext()
async function load() {
  const context = accountContext()
  loading.value = true
  try { const next = await get<AccessSettings>('/api/auth/access'); if (current(context)) { settings.value = next; username.value = next.username } }
  catch (cause) { if (current(context)) error.value = accessError(cause) }
  finally { if (current(context)) loading.value = false }
}
async function rename() {
  if (busy.value) return
  const context = accountContext()
  busy.value = true; error.value = ''; notice.value = ''
  try { await patch('/api/auth/access', { username: username.value, password: password.value }); if (!current(context)) return; await refreshSession(); if (!current(context)) return; await load(); if (current(context)) notice.value = 'Nombre de usuario guardado. Si tienes correo, también puedes seguir usándolo.' }
  catch (cause) { if (current(context)) error.value = accessError(cause) }
  finally { password.value = ''; busy.value = false }
}
async function register() {
  if (busy.value) return
  const context = accountContext()
  error.value = ''; notice.value = ''
  if (!validPIN(pin.value) || pin.value !== confirmation.value) { error.value = 'Escribe el mismo PIN de entre 6 y 12 números en ambos campos.'; return }
  busy.value = true
  try { await post('/api/auth/pin/device', { pin: pin.value, password: pinPassword.value, deviceName: deviceName.value }); if (!current(context)) return; await load(); if (current(context)) notice.value = 'Tu PIN está listo en este equipo. Al cerrar sesión, podrás volver con tu PIN durante 30 días.' }
  catch (cause) { if (current(context)) error.value = accessError(cause) }
  finally { pin.value = ''; confirmation.value = ''; pinPassword.value = ''; busy.value = false }
}
async function revoke() {
  if (!selected.value || busy.value) return
  const context = accountContext()
  busy.value = true; error.value = ''
  try { await remove('/api/auth/pin/devices/' + encodeURIComponent(selected.value.id)); if (!current(context)) return; await refreshSession(); if (!session.value) await router.replace('/login') }
  catch (cause) { if (current(context)) error.value = accessError(cause) }
  finally { busy.value = false }
}
onMounted(load)
onUnmounted(() => { active = false })
</script>
<template>
  <Card><CardHeader><CardTitle><h2>Mi acceso</h2></CardTitle><CardDescription>Aquí puedes revisar cómo entras a tu cuenta y los equipos que tienen acceso con PIN.</CardDescription></CardHeader><CardContent class="space-y-6">
    <p v-if="error" role="alert" class="rounded-lg bg-danger-muted p-3 text-sm leading-6 text-destructive">{{ error }}</p><p v-if="notice" role="status" class="rounded-lg bg-secondary p-3 text-sm leading-6 text-primary">{{ notice }}</p>
    <p v-if="loading && !settings" role="status" class="text-sm text-muted-foreground">Cargando opciones de acceso…</p><Button v-if="!loading && !settings" variant="outline" @click="load">Reintentar</Button>
    <template v-if="settings">
      <form v-if="settings.hasPassword" class="max-w-xl space-y-5" :aria-busy="busy" @submit.prevent="rename"><fieldset :disabled="busy" class="min-w-0 space-y-5"><div class="space-y-2"><Label for="access-username">Mi nombre de usuario</Label><Input id="access-username" v-model="username" autocomplete="username" autocapitalize="none" :spellcheck="false" pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,39}" minlength="3" maxlength="40" required /><p class="text-base leading-7 text-muted-foreground">Puedes entrar con este nombre o con tu correo, si lo tienes.</p></div><div class="space-y-2"><Label for="access-password">Confirma tu contraseña actual</Label><PasswordInput id="access-password" v-model="password" autocomplete="current-password" maxlength="256" required class="form-input" :disabled="busy" visibility-label="contraseña actual" /></div><Button type="submit">Guardar nombre de usuario</Button></fieldset></form>
      <p v-else class="text-base leading-7 text-muted-foreground">Puedes entrar con tu PIN sin recordar un usuario o correo. Si lo olvidas o cambias de equipo, pide a quien administra tu hogar un nuevo enlace desde Mi familia → Preparar acceso con ayuda. Tu cuenta y tus documentos se conservarán.</p>
      <div class="border-t pt-6"><h3 class="font-semibold text-heading">Acceso con PIN</h3><p v-if="!settings.canUsePIN" class="mt-3 text-base leading-7 text-muted-foreground">Para las cuentas de administración o con permisos de gestión y copias se conserva el acceso con contraseña. Para ayudar a otro miembro, ve a Mi familia → Preparar acceso con ayuda.</p>
      <form v-else-if="settings.hasPassword" class="mt-4 max-w-xl space-y-5" :aria-busy="busy" @submit.prevent="register"><fieldset :disabled="busy" class="min-w-0 space-y-5"><p class="text-base leading-7 text-muted-foreground">Hazlo en tu teléfono o computadora personal. El PIN funcionará aquí, en este mismo navegador, durante 30 días. Si lo olvidas, pide ayuda: cinco errores lo bloquean.</p><div class="space-y-2"><Label for="pin-device-name">Nombre de este equipo</Label><Input id="pin-device-name" v-model="deviceName" maxlength="80" required /></div><PINField id="device-pin" v-model="pin" label="Elige tu PIN" help="Entre 6 y 12 números, sin letras." autocomplete="new-password" /><PINField id="device-pin-confirm" v-model="confirmation" label="Repite tu PIN" help="El mismo número que acabas de elegir." autocomplete="new-password" /><div class="space-y-2"><Label for="pin-account-password">Tu contraseña actual</Label><PasswordInput id="pin-account-password" v-model="pinPassword" autocomplete="current-password" maxlength="256" required class="form-input" :disabled="busy" visibility-label="contraseña actual" /></div><Button type="submit">Activar PIN en este equipo</Button></fieldset></form><p v-else class="mt-3 text-base leading-7 text-muted-foreground">Cuando cierres sesión, vuelve a abrir HomeAtlas en este mismo navegador para entrar con tu PIN. Este acceso dura 30 días; después necesitarás ayuda para prepararlo de nuevo.</p></div>
      <div><h3 class="font-semibold text-heading">Mis equipos con PIN</h3><p v-if="!settings.devices.length" class="mt-3 text-sm text-muted-foreground">Todavía no has preparado un equipo para entrar con PIN.</p><ul v-else class="mt-3 divide-y"><li v-for="device in settings.devices" :key="device.id" class="flex flex-wrap items-center justify-between gap-4 py-4"><div class="min-w-0"><p class="break-words text-sm font-medium">{{ device.name }}</p><p class="mt-1 text-base leading-7 text-muted-foreground">{{ device.locked ? 'PIN bloqueado · ' : '' }}Disponible hasta {{ formatHouseholdDate(device.expiresAt, true) }}</p></div><Button variant="outline" size="sm" :disabled="busy" :aria-label="'Desactivar PIN de ' + device.name" @click="selected = device">Desactivar PIN</Button></li></ul></div>
    </template>
  </CardContent></Card>
  <ActionDialog v-if="selected" title="Desactivar el PIN de este equipo" :description="selected.name" :busy="busy" @close="selected = undefined"><div class="space-y-6"><p class="text-base leading-7 text-muted-foreground">Este equipo ya no podrá entrar con PIN. También se cerrará tu sesión aquí y en los demás equipos. Tus documentos se conservarán. Para volver, usa tu contraseña o pide a quien administra tu hogar un nuevo enlace para preparar el PIN.</p><p v-if="error" role="alert" class="text-sm text-destructive">{{ error }}</p><div class="flex flex-wrap gap-3"><Button variant="outline" :disabled="busy" @click="selected = undefined">Cancelar</Button><Button variant="destructive" :disabled="busy" @click="revoke">Desactivar PIN y cerrar sesiones</Button></div></div></ActionDialog>
</template>
