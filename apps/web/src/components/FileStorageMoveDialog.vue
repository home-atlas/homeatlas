<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ApiError, get, post } from '@/core/api'
import { formatSize } from '@/core/format-size'
import ActionDialog from '@/components/ActionDialog.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
interface Stats { bucket?: string; target: 'local' | 's3'; revision: string; local: number; remote: number; localBytes: number; remoteBytes: number; pendingCleanup: number }
interface Result extends Stats { moved: boolean }
const emit = defineEmits<{ close: [] }>()
const stats = ref<Stats>(), loading = ref(true), running = ref(false), confirmed = ref(false), stopped = ref(false), error = ref(''), notice = ref(''), moved = ref(0)
let alive = true
const remaining = computed(() => !stats.value ? 0 : stats.value.target === 's3' ? stats.value.local : stats.value.remote)
const bytes = computed(() => !stats.value ? 0 : stats.value.target === 's3' ? stats.value.localBytes : stats.value.remoteBytes)
const target = computed(() => stats.value?.target === 's3' ? 'S3' : 'Local')
function message(cause: unknown) {
  if (cause instanceof ApiError && cause.code === 'storage_settings_changed') return 'El destino cambió. Actualiza la revisión antes de continuar.'
  if (cause instanceof ApiError && cause.status === 423) return 'Hay otra operación en curso. Vuelve a intentarlo cuando termine.'
  if (cause instanceof ApiError && [401, 403].includes(cause.status)) return 'Tu sesión o permiso cambió. Vuelve a iniciar sesión para continuar.'
  return 'No se pudo completar el traslado. Los archivos ya trasladados siguen disponibles; los pendientes conservan su origen. Comprueba la conexión, los permisos y el espacio disponible y vuelve a revisar.'
}
async function review() {
  loading.value = true; error.value = ''; notice.value = ''; confirmed.value = false
  try { const value = await get<Stats>('/api/household/storage/move'); if (alive) stats.value = value }
  catch (cause) { if (alive) error.value = message(cause) }
  finally { if (alive) loading.value = false }
}
async function move() {
  if (!stats.value || running.value || !confirmed.value || (!remaining.value && !stats.value.pendingCleanup)) return
  const revision = stats.value.revision
  running.value = true; stopped.value = false; error.value = ''; notice.value = ''; moved.value = 0
  try {
    do {
      const result: Result = await post<Result>('/api/household/storage/move', { revision, confirm: 'move-and-remove-source' }, 120000)
      if (!alive) return
      stats.value = result
      if (result.moved) moved.value++
      if (result.pendingCleanup) { notice.value = 'La copia verificada ya está activa, pero queda retirar el original anterior. Revisa los permisos y reintenta la limpieza.'; break }
    } while (remaining.value > 0 && !stopped.value)
    if (!notice.value) notice.value = stopped.value ? 'Traslado detenido al terminar el archivo en curso. Puedes continuar con los pendientes.' : 'Traslado terminado. Los archivos y sus permisos se conservan.'
  } catch (cause) { if (alive) error.value = message(cause) }
  finally { if (alive) { running.value = false; confirmed.value = false } }
}
function returnFocus() { return document.getElementById('file-storage-move') }
onMounted(review)
onBeforeUnmount(() => { alive = false; stopped.value = true })
</script>
<template>
  <ActionDialog title="Mover documentos existentes" description="Primero guarda el destino en Configurar almacenamiento. Aquí puedes trasladar los documentos que siguen en el otro proveedor." :busy="running" :return-focus="returnFocus" @close="emit('close')">
    <div class="space-y-5" :aria-busy="loading || running">
      <p v-if="loading" role="status" class="text-sm text-muted-foreground">Revisando documentos…</p>
      <template v-else-if="stats">
        <dl class="grid grid-cols-2 gap-4 text-sm"><div><dt class="text-muted-foreground">Destino guardado</dt><dd class="mt-1 font-semibold">{{ stats.target === 's3' && stats.bucket ? `S3 · ${stats.bucket}` : target }}</dd></div><div><dt class="text-muted-foreground">Pendientes</dt><dd class="mt-1 font-semibold">{{ remaining }} · {{ formatSize(bytes) }}</dd></div><div><dt class="text-muted-foreground">En Local</dt><dd class="mt-1">{{ stats.local }}</dd></div><div><dt class="text-muted-foreground">En S3</dt><dd class="mt-1">{{ stats.remote }}</dd></div></dl>
        <p class="text-sm leading-6 text-muted-foreground">Cada copia se verifica antes de cambiar su ubicación y retirar el original. Se conservan los nombres, la búsqueda y los permisos. Durante cada archivo, otras operaciones pueden quedar en espera.</p>
        <p v-if="!remaining && !stats.pendingCleanup" role="status" class="text-sm text-muted-foreground">No hay documentos pendientes para este destino.</p>
        <Alert v-if="stats.pendingCleanup"><AlertDescription>Hay {{ stats.pendingCleanup }} originales anteriores pendientes de limpieza. Sus copias verificadas ya están disponibles.</AlertDescription></Alert>
        <Label v-if="remaining || stats.pendingCleanup" for="storage-move-confirm" class="flex min-h-11 items-start gap-3 leading-6"><Checkbox id="storage-move-confirm" v-model="confirmed" :disabled="running" class="mt-1" />Mover al destino indicado y retirar los originales verificados.</Label>
        <p v-if="running || moved" role="status" aria-live="polite" class="text-sm text-primary">{{ running ? 'Trasladando…' : 'Trasladados en esta ejecución:' }} {{ moved }} · {{ remaining }} pendientes</p>
      </template>
      <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <p v-if="notice" role="status" class="text-sm leading-6 text-primary">{{ notice }}</p>
      <Button v-if="!running && !loading" variant="outline" @click="review">Actualizar revisión</Button>
    </div>
    <template #footer><div class="flex flex-wrap justify-end gap-3"><Button v-if="running" variant="outline" :disabled="stopped" @click="stopped = true">{{ stopped ? 'Deteniendo…' : 'Detener después de este archivo' }}</Button><Button v-else variant="outline" @click="emit('close')">Cerrar</Button><Button v-if="!running" :disabled="loading || !confirmed || (!remaining && !stats?.pendingCleanup)" @click="move">{{ stats?.pendingCleanup ? 'Reintentar limpieza' : `Mover a ${target}` }}</Button></div></template>
  </ActionDialog>
</template>
