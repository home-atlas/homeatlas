<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { get, post, remove } from '@/core/api'
import { refreshSession, session } from '@/core/auth'
import { accountContext, accountError, type AccountSession, type AccountSessions } from '@/core/account'
import { formatHouseholdDate } from '@/core/datetime'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import ActionDialog from '@/components/ActionDialog.vue'

const router = useRouter(), data = ref<AccountSessions>(), loading = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const selected = ref<{ kind: 'one'; item: AccountSession } | { kind: 'others' }>()
let revision = 0
async function load() {
  const request = ++revision, context = accountContext()
  loading.value = true; data.value = undefined; error.value = ''
  try {
    const next = await get<AccountSessions>('/api/auth/sessions')
    if (request === revision && context === accountContext()) data.value = next
  } catch (cause) { if (request === revision && context === accountContext()) error.value = accountError(cause) }
  finally { if (request === revision) loading.value = false }
}
async function revoke() {
  if (!selected.value || busy.value) return
  const target = selected.value, context = accountContext(), request = ++revision
  const current = () => request === revision && context === accountContext()
  busy.value = true; error.value = ''; notice.value = ''
  try {
    if (target.kind === 'others') await post('/api/auth/sessions/revoke-others')
    else await remove('/api/auth/sessions/' + encodeURIComponent(target.item.id))
    if (!current()) return
    selected.value = undefined
    if (target.kind === 'one' && target.item.current) { await refreshSession(); if (!session.value) await router.replace('/login'); return }
    await load(); notice.value = 'Sesiones actualizadas.'
  } catch (cause) { if (current()) error.value = accountError(cause) }
  finally { if (context === accountContext()) busy.value = false }
}
onMounted(load)
onUnmounted(() => { ++revision })
</script>
<template>
  <Card><CardHeader><CardTitle><h2>Mis sesiones</h2></CardTitle><CardDescription>Accesos activos de tu cuenta a este hogar. Cerrar una sesión conserva tus documentos.</CardDescription></CardHeader><CardContent class="space-y-5">
    <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
    <p v-if="notice" role="status" class="text-sm text-primary">{{ notice }}</p>
    <p v-if="loading" role="status" class="text-sm text-muted-foreground">Consultando sesiones…</p>
    <Button v-else-if="!data" variant="outline" @click="load">Reintentar sesiones</Button>
    <template v-if="data">
      <p v-if="data.more" class="text-sm text-muted-foreground">Mostramos las 100 sesiones más recientes, incluida la actual. Puedes cerrar todas las demás de una vez.</p>
      <ul class="divide-y"><li v-for="item in data.sessions" :key="item.id" class="flex flex-wrap items-center justify-between gap-4 py-4">
        <div class="min-w-0 space-y-1"><div class="flex flex-wrap items-center gap-2"><p class="font-medium">{{ item.method === 'pin' ? 'Acceso con PIN' : 'Acceso con contraseña' }}</p><Badge v-if="item.current" variant="secondary">Esta sesión</Badge></div><p class="text-sm leading-6 text-muted-foreground">Última actividad: {{ formatHouseholdDate(item.lastSeenAt, true) }}</p><p class="text-sm leading-6 text-muted-foreground">Caduca: {{ formatHouseholdDate(item.expiresAt, true) }}</p></div>
        <Button variant="outline" :disabled="busy || loading" :aria-label="item.current ? 'Cerrar esta sesión' : 'Cerrar sesión del ' + formatHouseholdDate(item.lastSeenAt, true)" @click="selected = { kind: 'one', item }">{{ item.current ? 'Cerrar esta sesión' : 'Cerrar sesión' }}</Button>
      </li></ul>
      <div class="flex flex-wrap gap-3"><Button variant="outline" :disabled="busy || loading" @click="load">Actualizar sesiones</Button><Button variant="outline" :disabled="busy || loading || (data.sessions.length < 2 && !data.more)" @click="selected = { kind: 'others' }">Cerrar las demás sesiones</Button></div>
      <p class="text-sm leading-6 text-muted-foreground">Las sesiones terminan tras 30 minutos sin actividad o, como máximo, a las 12 horas. Los equipos con PIN siguen vinculados hasta desactivarlos en Mi acceso.</p>
    </template>
  </CardContent></Card>
  <ActionDialog v-if="selected" :title="selected.kind === 'others' ? 'Cerrar las demás sesiones' : 'Cerrar la sesión seleccionada'" description="Esta acción solo afecta a tus accesos a este hogar." :busy="busy" @close="selected = undefined">
    <div class="space-y-6"><p class="leading-7">{{ selected.kind === 'others' ? 'Conservarás la sesión actual en este navegador. Los demás accesos tendrán que iniciar sesión de nuevo.' : selected.item.current ? 'Saldrás de HomeAtlas en este navegador. Podrás volver con tu contraseña o con un PIN vinculado.' : 'Ese acceso tendrá que iniciar sesión de nuevo. Tu sesión actual seguirá abierta.' }}</p><p v-if="error" role="alert" class="text-sm text-destructive">{{ error }}</p><div class="flex flex-wrap gap-3"><Button variant="outline" :disabled="busy" @click="selected = undefined">Cancelar</Button><Button variant="destructive" :disabled="busy" @click="revoke">Confirmar cierre</Button></div></div>
  </ActionDialog>
</template>
