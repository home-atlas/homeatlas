<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ApiError, get, patch } from '@/core/api'
import { refreshSession, session } from '@/core/auth'
import { canManageHousehold, householdError } from '@/core/household'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import AccountAccessCard from '@/components/AccountAccessCard.vue'
import FileStorageCard from '@/components/FileStorageCard.vue'
import HouseholdBackupCard from '@/components/HouseholdBackupCard.vue'
import HouseholdBackupScheduleCard from '@/components/HouseholdBackupScheduleCard.vue'
import { canBackupHousehold } from '@/core/backups'

interface Settings { household: { id: string; name: string; timezone: string }; canManage: boolean }
const router = useRouter()
const settings = ref<Settings>(), loading = ref(true), busy = ref(false), error = ref(''), notice = ref('')
const form = reactive({ name: '', timezone: 'UTC' })
const canManage = computed(() => settings.value?.canManage && canManageHousehold(session.value))
const timezones = computed(() => [...new Set(['UTC', form.timezone, ...Intl.supportedValuesOf('timeZone')])].sort())
async function load() {
  loading.value = true; error.value = ''
  try { settings.value = await get<Settings>('/api/household/settings'); Object.assign(form, { name: settings.value.household.name, timezone: settings.value.household.timezone }) }
  catch (cause) { settings.value = undefined; error.value = householdError(cause) }
  finally { loading.value = false }
}
async function save() {
  if (busy.value || !canManage.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await patch<void>('/api/household/settings', { name: form.name, timezone: form.timezone })
    await refreshSession(); await load()
    notice.value = 'Configuración del hogar guardada.'
  } catch (cause) {
    error.value = cause instanceof ApiError && cause.code === 'invalid_household_settings' ? 'Revisa el nombre y elige una zona horaria válida.' : householdError(cause)
    await refreshSession().catch(() => {})
    if (!session.value) await router.replace('/login?redirect=%2Fsettings')
  } finally { busy.value = false }
}
onMounted(load)
</script>

<template>
  <section aria-labelledby="settings-title" class="space-y-7">
    <div><p class="mb-3 text-sm text-primary">Un espacio propio</p><h1 id="settings-title" class="text-3xl font-semibold tracking-tight">Configuración</h1><p class="mt-3 text-sm leading-6 text-muted-foreground">Ajustes del hogar {{ session?.household.name }}.</p></div>
    <p v-if="error" role="alert" class="rounded-xl border border-destructive/30 bg-card p-4 text-sm text-destructive">{{ error }}</p>
    <p v-if="notice" role="status" class="rounded-xl bg-secondary p-4 text-sm text-primary">{{ notice }}</p>
    <p v-if="loading" aria-live="polite" class="text-sm text-muted-foreground">Cargando configuración…</p>
    <Button v-else-if="!settings" variant="outline" @click="load">Reintentar</Button>
    <template v-if="settings">
      <AccountAccessCard />
      <Card><CardHeader><CardTitle>Tu hogar</CardTitle><CardDescription>Nombre y zona horaria compartidos por los miembros de este hogar.</CardDescription></CardHeader><CardContent><form class="max-w-xl space-y-6" :aria-busy="busy" @submit.prevent="save"><fieldset :disabled="busy || !canManage" class="space-y-5"><div class="space-y-2"><label for="household-settings-name" class="form-label">Nombre del hogar</label><input id="household-settings-name" v-model="form.name" name="name" maxlength="100" required class="form-input" /></div><div class="space-y-2"><label for="household-settings-timezone" class="form-label">Zona horaria del hogar</label><select id="household-settings-timezone" v-model="form.timezone" name="timezone" class="form-input" required><option v-for="zone in timezones" :key="zone" :value="zone">{{ zone }}</option></select></div></fieldset><p v-if="!canManage" class="text-sm leading-6 text-muted-foreground">Puedes consultar estos ajustes. Solo el propietario o un administrador con permiso de gestión puede cambiarlos.</p><Button v-if="canManage" type="submit" :disabled="busy">{{ busy ? 'Guardando…' : 'Guardar configuración' }}</Button></form></CardContent></Card>
      <Card><CardHeader><CardTitle>Módulos y miembros</CardTitle><CardDescription>Los cambios de módulos se aplican únicamente a este hogar y conservan sus datos.</CardDescription></CardHeader><CardContent class="flex flex-wrap gap-3"><Button as-child variant="outline"><RouterLink to="/modules">Gestionar módulos</RouterLink></Button><Button as-child variant="outline"><RouterLink to="/family">Mi familia</RouterLink></Button></CardContent></Card>
      <FileStorageCard />
      <HouseholdBackupCard @restored="load" />
      <HouseholdBackupScheduleCard v-if="canBackupHousehold(session)" />
    </template>
  </section>
</template>
