<script setup lang="ts">
import { PasswordInput } from '@/components/ui/input'
import { computed, onMounted, reactive, ref } from 'vue'
import { session } from '@/core/auth'
import { backupError, canBackupHousehold, validBackupPassword } from '@/core/backups'
import { backupHistoryError, backupHistoryStatus, downloadHistoryBackup, getBackupHistory, getBackupSchedule, runBackupSchedule, saveBackupSchedule, type BackupHistoryEntry, type BackupSchedule } from '@/core/backup-schedule'
import { formatHouseholdDate } from '@/core/datetime'
import { formatSize } from '@/core/format-size'
import ActionDialog from '@/components/ActionDialog.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

const allowed = computed(() => canBackupHousehold(session.value))
const schedule = ref<BackupSchedule>()
const form = reactive({ enabled: false, intervalHours: 24 as 24 | 168, keep: 7 })
const password = ref(''), confirmation = ref('')
const history = ref<BackupHistoryEntry[]>([]), total = ref(0)
const loading = ref(true), busy = ref(false), confirming = ref(false), error = ref(''), notice = ref('')
const valid = computed(() => Number.isInteger(form.keep) && form.keep >= 1 && form.keep <= 30 && (!form.enabled || (!password.value && schedule.value?.passwordConfigured) || (validBackupPassword(password.value) && password.value === confirmation.value)))
async function refreshHistory(more = false) {
  const page = await getBackupHistory(more ? history.value.length : 0)
  history.value = more ? [...history.value, ...page.items].filter((item, index, items) => items.findIndex(other => other.id === item.id) === index) : page.items
  total.value = page.total
}
async function load() {
  if (!allowed.value) return
  loading.value = true; error.value = ''
  try {
    const [current] = await Promise.all([getBackupSchedule(), refreshHistory()])
    schedule.value = current
    Object.assign(form, { enabled: current.enabled, intervalHours: current.intervalHours, keep: current.keep })
  } catch (cause) { error.value = backupError(cause) }
  finally { loading.value = false }
}
async function save() {
  if (busy.value || !allowed.value || !valid.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    schedule.value = await saveBackupSchedule({ ...form }, password.value)
    password.value = ''; confirmation.value = ''; confirming.value = false
    notice.value = form.enabled ? 'Programación guardada. Conserva la contraseña por separado para recuperar las copias fuera de este servidor.' : 'Programación desactivada y contraseña guardada eliminada. Las copias existentes se conservan.'
  } catch (cause) { error.value = backupError(cause) }
  finally { busy.value = false }
}
async function runNow() {
  if (busy.value || !allowed.value || !schedule.value?.enabled) return
  busy.value = true; error.value = ''; notice.value = ''
  let failure = ''
  try { await runBackupSchedule(); notice.value = 'Copia cifrada creada. Ya puedes descargarla desde el historial.' }
  catch (cause) { failure = backupError(cause) }
  finally { await load(); if (failure) error.value = failure; busy.value = false }
}
async function refresh(more = false) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await refreshHistory(more) }
  catch (cause) { error.value = backupError(cause) }
  finally { busy.value = false }
}
async function downloadEntry(entry: BackupHistoryEntry) {
  if (busy.value || !allowed.value || !entry.available) return
  busy.value = true; error.value = ''
  try {
    const blob = await downloadHistoryBackup(entry.id), url = URL.createObjectURL(blob), link = document.createElement('a')
    link.href = url; link.download = `homeatlas-complete-${entry.id}.zip.age`; link.click()
    setTimeout(() => URL.revokeObjectURL(url), 60000)
  } catch (cause) { error.value = backupError(cause) }
  finally { busy.value = false }
}
onMounted(load)
</script>

<template>
  <Card v-if="allowed">
    <CardHeader><CardTitle>Copias automáticas e historial</CardTitle><CardDescription>Crea copias cifradas en este servidor y conserva las más recientes.</CardDescription></CardHeader>
    <CardContent class="space-y-6" :aria-busy="busy || loading">
      <p class="text-sm leading-6 text-muted-foreground">El servidor debe permanecer encendido. Las copias locales también se pierden si pierdes el servidor: descarga las importantes y guárdalas fuera de él. Estas copias recuperan el hogar y sus miembros igual que una copia completa manual.</p>
      <p v-if="loading" role="status" class="text-sm text-muted-foreground">Cargando programación e historial…</p>
      <Button v-else-if="!schedule" variant="outline" @click="load">Reintentar</Button>
      <template v-else>
        <form class="space-y-5" @submit.prevent="confirming = true">
          <fieldset :disabled="busy" class="space-y-5">
            <label class="form-option"><input v-model="form.enabled" type="checkbox" class="form-checkbox" />Activar copias automáticas</label>
            <div class="grid gap-5 sm:grid-cols-2">
              <div class="space-y-2"><label for="backup-schedule-frequency" class="form-label">Frecuencia</label><select id="backup-schedule-frequency" v-model.number="form.intervalHours" class="form-input"><option :value="24">Cada 24 horas</option><option :value="168">Cada 7 días</option></select></div>
              <div class="space-y-2"><label for="backup-schedule-keep" class="form-label">Copias que se conservarán</label><input id="backup-schedule-keep" v-model.number="form.keep" type="number" min="1" max="30" required class="form-input" aria-describedby="backup-retention-help" /></div>
            </div>
            <p id="backup-retention-help" class="text-xs leading-5 text-muted-foreground">Entre 1 y 30. Las copias anteriores se retiran solo después de crear otra correctamente. Los fallos nunca eliminan la última copia buena. No afecta las descargas manuales ni la copia previa a una restauración.</p>
            <template v-if="form.enabled">
              <div class="space-y-2"><label for="backup-schedule-password" class="form-label">{{ schedule.passwordConfigured ? 'Nueva contraseña de las próximas copias (opcional)' : 'Contraseña de las copias automáticas' }}</label><PasswordInput id="backup-schedule-password" v-model="password" autocomplete="new-password" maxlength="256" :required="!schedule.passwordConfigured" class="form-input" aria-describedby="backup-schedule-secret-help" :disabled="busy" visibility-label="contraseña de copias automáticas" /><p id="backup-schedule-secret-help" class="text-xs leading-5 text-muted-foreground">Mínimo 12 caracteres. Para automatizar, la contraseña se guarda cifrada con una clave privada de este servidor. Guárdala también por separado. Si la cambias, las copias anteriores conservan su contraseña anterior.</p></div>
              <div class="space-y-2"><label for="backup-schedule-password-confirm" class="form-label">Repite la nueva contraseña</label><PasswordInput id="backup-schedule-password-confirm" v-model="confirmation" autocomplete="new-password" maxlength="256" :required="!!password" class="form-input" :disabled="busy" visibility-label="confirmación de contraseña de copias automáticas" /></div>
            </template>
            <div class="flex flex-wrap gap-3"><Button type="submit" :disabled="busy || !valid">Revisar programación</Button><Button type="button" variant="outline" :disabled="busy || !schedule.enabled" @click="runNow">{{ busy ? 'Procesando…' : 'Crear copia ahora' }}</Button></div>
          </fieldset>
        </form>
        <p class="text-sm leading-6 text-muted-foreground">{{ schedule.enabled && schedule.nextRunAt ? `Próxima copia: ${formatHouseholdDate(schedule.nextRunAt, true)} (${session?.household.timezone}).` : 'La programación está desactivada.' }} El intervalo empieza al activarla, cambiar la frecuencia o ejecutar una copia. Si el servidor estaba apagado, se hará una sola copia pendiente al volver a iniciar.</p>
        <div class="space-y-4 border-t pt-6">
          <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="font-semibold text-heading">Historial de copias automáticas</h3><Button variant="outline" :disabled="busy" @click="refresh()">Actualizar historial</Button></div>
          <p v-if="!history.length" class="text-sm text-muted-foreground">Aún no hay copias automáticas. Activa la programación y crea la primera copia.</p>
          <ul v-else class="space-y-4">
            <li v-for="entry in history" :key="entry.id" class="space-y-3 rounded-xl border p-4">
              <div class="flex flex-wrap items-center justify-between gap-3"><div><p class="text-sm font-semibold text-heading">{{ formatHouseholdDate(entry.startedAt, true) }}</p><p class="mt-1 text-sm text-muted-foreground">{{ entry.trigger === 'scheduled' ? 'Programada' : 'Solicitada desde la app' }} · {{ backupHistoryStatus(entry) }}<template v-if="entry.sizeBytes"> · {{ formatSize(entry.sizeBytes) }}</template></p></div><Button v-if="entry.available" variant="outline" :disabled="busy" @click="downloadEntry(entry)">Descargar copia cifrada</Button></div>
              <p v-if="entry.errorCode" class="text-sm leading-6 text-muted-foreground">{{ backupHistoryError(entry.errorCode) }}</p>
            </li>
          </ul>
          <Button v-if="history.length < total" variant="outline" :disabled="busy" @click="refresh(true)">Ver más intentos</Button>
          <p class="text-xs leading-5 text-muted-foreground">Se conservan las copias retenidas y los últimos 200 intentos. Las copias retiradas ya no se pueden descargar. Desactivar la programación conserva los archivos del historial.</p>
        </div>
      </template>
      <p v-if="error && !confirming" role="alert" class="text-sm leading-6 text-destructive">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm leading-6 text-primary">{{ notice }}</p>
    </CardContent>
  </Card>
  <ActionDialog v-if="confirming" title="Guardar programación de copias" description="Solo para este hogar" :busy="busy" @close="confirming = false">
    <div class="space-y-5">
      <template v-if="form.enabled"><p class="text-sm leading-6">Se creará una copia cifrada cada {{ form.intervalHours === 24 ? '24 horas' : '7 días' }} y se conservarán las últimas {{ form.keep }} copias correctas. Después de cada copia correcta se eliminarán del servidor las anteriores que superen ese límite.</p><p class="text-sm leading-6">Para ejecutarse sin tu intervención, el servidor guardará la contraseña cifrada con su clave privada local. Quien tenga acceso completo al servidor puede acceder a esa clave. Guarda la contraseña por separado para recuperar tu hogar en otro servidor.</p></template>
      <p v-else class="text-sm leading-6">Se detendrán las copias automáticas y se eliminará la contraseña guardada para esta programación. Las copias existentes y su historial se conservarán.</p>
      <p v-if="error" role="alert" class="text-sm leading-6 text-destructive">{{ error }}</p>
      <div class="flex flex-wrap gap-3"><Button variant="outline" :disabled="busy" @click="confirming = false">Cancelar</Button><Button :disabled="busy || !valid" @click="save">{{ busy ? 'Guardando…' : 'Confirmar cambios' }}</Button></div>
    </div>
  </ActionDialog>
</template>
