<script setup lang="ts">
import { PasswordInput } from '@/components/ui/input'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { refreshSession, session } from '@/core/auth'
import { refreshModules } from '@/core/state'
import { backupError, canBackupHousehold, createBackup, downloadPreviousBackup, maxBackupBytes, restoreBackup, validBackupPassword } from '@/core/backups'
import ActionDialog from '@/components/ActionDialog.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

const emit = defineEmits<{ restored: [] }>()
const router = useRouter()
const allowed = computed(() => canBackupHousehold(session.value))
const busy = ref(false), confirming = ref(false), error = ref(''), notice = ref('')
const password = ref(''), confirmation = ref(''), restorePassword = ref('')
const passwordReady = computed(() => validBackupPassword(password.value) && password.value === confirmation.value)
const selected = ref<File>(), input = ref<HTMLInputElement>()
function selectFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  selected.value = undefined; error.value = ''; notice.value = ''
  if (!file) return
  if (!file.size || file.size > maxBackupBytes) { error.value = 'Elige una copia .zip.age o ZIP de HomeAtlas de hasta 1 GiB.'; return }
  selected.value = file
}
function save(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob), link = document.createElement('a')
  link.href = url; link.download = filename; link.click()
  setTimeout(() => URL.revokeObjectURL(url), 60000)
}
async function exportBackup(previous = false) {
  if (busy.value || !allowed.value || (!previous && !passwordReady.value)) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const blob = previous ? await downloadPreviousBackup(passwordReady.value ? password.value : '') : await createBackup(password.value)
    save(blob, previous ? 'homeatlas-before-restore.zip.age' : `homeatlas-complete-${new Date().toISOString().replace(/[:.]/g, '-')}.zip.age`)
    notice.value = previous ? 'Copia previa preparada. Usa la contraseña de la última restauración; si era una copia antigua, usa la contraseña que acabas de indicar.' : 'Copia cifrada preparada. Guarda la contraseña por separado: HomeAtlas no puede recuperarla.'
    password.value = ''; confirmation.value = ''
  } catch (cause) { error.value = backupError(cause) }
  finally { busy.value = false }
}
async function restore() {
  if (busy.value || !selected.value || !allowed.value || !validBackupPassword(restorePassword.value)) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const result = await restoreBackup(selected.value, restorePassword.value)
    confirming.value = false; selected.value = undefined; restorePassword.value = ''
    if (input.value) input.value.value = ''
    notice.value = 'Datos del hogar y de los módulos incluidos restaurados. La copia anterior está disponible para descargar.'
    if (result.unavailableOriginals) notice.value += ` La copia restaurada declara ${result.unavailableOriginals} originales no disponibles; se conservaron sus metadatos.`
    if (result.previousUnavailableOriginals) notice.value += ` La copia previa declara ${result.previousUnavailableOriginals} originales que ya faltaban o estaban dañados.`
    await refreshSession(); await refreshModules(router, true); emit('restored')
  } catch (cause) { error.value = backupError(cause) }
  finally { busy.value = false }
}
</script>

<template>
  <Card>
    <CardHeader><CardTitle>Copias de seguridad</CardTitle><CardDescription>Datos del hogar y de sus módulos, incluso si están desactivados.</CardDescription></CardHeader>
    <CardContent class="space-y-6" :aria-busy="busy">
      <p class="text-sm leading-6 text-muted-foreground">La copia completa v4 incluye hogar, cuentas activas, roles, permisos, nombres de usuario, hashes de contraseña y los datos y archivos privados de los módulos instalados. Se cifra íntegramente con una contraseña y se descarga como .zip.age. Esta descarga manual no guarda la contraseña. Consérvala por separado para recuperar la copia. La programación automática tiene su propia contraseña guardada y protegida en este servidor. No contiene contraseñas en texto claro, PIN de dispositivos, sesiones, invitaciones, enlaces de alta ni secretos SMTP. Los miembros con acceso asistido deberán vincular de nuevo sus equipos tras recuperar una instalación.</p>
      <p v-if="!allowed" class="text-sm leading-6 text-muted-foreground">Solo el propietario con permisos de gestión y respaldo puede crear o restaurar una copia del hogar.</p>
      <template v-else>
        <form class="space-y-5" @submit.prevent="exportBackup()">
          <fieldset :disabled="busy" class="space-y-5">
            <div class="space-y-2"><label for="backup-password" class="form-label">Contraseña para cifrar la copia</label><PasswordInput id="backup-password" v-model="password" autocomplete="new-password" required minlength="12" maxlength="256" class="form-input" aria-describedby="backup-password-help" :disabled="busy" visibility-label="contraseña de la copia" /><p id="backup-password-help" class="text-xs leading-5 text-muted-foreground">Al menos 12 caracteres, hasta 256 bytes. Usa una frase larga y única. También puedes completar estos campos para descargar cifrada una copia previa de una versión antigua.</p></div>
            <div class="space-y-2"><label for="backup-password-confirm" class="form-label">Repite la contraseña de la copia</label><PasswordInput id="backup-password-confirm" v-model="confirmation" autocomplete="new-password" required minlength="12" maxlength="256" class="form-input" :disabled="busy" visibility-label="confirmación de contraseña de la copia" /></div>
            <div class="flex flex-wrap gap-3"><Button type="submit" :disabled="busy || !passwordReady">{{ busy ? 'Procesando…' : 'Crear y descargar copia cifrada' }}</Button><Button type="button" variant="outline" :disabled="busy" @click="exportBackup(true)">Descargar copia previa a restauración</Button></div>
          </fieldset>
        </form>
        <div class="space-y-3 border-t pt-6">
          <label for="household-backup-file" class="form-label">Restaurar una copia de este hogar</label>
          <input id="household-backup-file" ref="input" type="file" accept=".age,.zip,application/zip,application/octet-stream" class="form-file" :disabled="busy" aria-describedby="household-backup-help" @change="selectFile" />
          <p id="household-backup-help" class="text-sm leading-6 text-muted-foreground">Hasta 1 GiB. Aquí se reemplazan datos del hogar conservando las cuentas, contraseñas, roles y capacidades actuales. Los accesos compartidos se conservan solo si aparecen en la copia y siguen vigentes. Para recuperar todo en un servidor nuevo, usa esta copia completa desde el alta inicial de la instalación vacía.</p>
          <p class="text-sm leading-6 text-muted-foreground">Se conserva una sola copia previa en el servidor; cada intento de restauración validado puede reemplazarla. Descárgala si deseas guardarla.</p>
          <div class="space-y-2"><label for="restore-backup-password" class="form-label">Contraseña para restaurar</label><PasswordInput id="restore-backup-password" v-model="restorePassword" autocomplete="off" minlength="12" maxlength="256" :disabled="busy" class="form-input" aria-describedby="restore-password-help" visibility-label="contraseña para restaurar" /><p id="restore-password-help" class="text-xs leading-5 text-muted-foreground">Para .zip.age, usa su contraseña. Para un ZIP antiguo, elige una nueva de al menos 12 caracteres. La copia previa se cifrará con esa misma contraseña; guárdala antes de continuar.</p></div>
          <Button variant="outline" :disabled="busy || !selected || !validBackupPassword(restorePassword)" @click="confirming = true">Revisar restauración</Button>
        </div>
      </template>
      <p v-if="error && !confirming" role="alert" class="text-sm leading-6 text-destructive">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm leading-6 text-primary">{{ notice }}</p>
    </CardContent>
  </Card>
  <ActionDialog v-if="confirming" title="Restaurar el hogar" :description="session?.household.name ?? ''" :busy="busy" @close="confirming = false">
    <div class="space-y-5">
      <p class="break-words text-sm leading-6">Copia seleccionada: <strong>{{ selected?.name }}</strong></p>
      <p class="text-sm leading-6">Se validará la integridad de SQLite y de cada archivo antes de aplicar cambios. Se reemplazarán el nombre y la zona horaria del hogar, y los datos y ajustes de los módulos incluidos en la copia. Los módulos ausentes de la copia conservan sus datos y ajustes actuales.</p>
      <p class="text-sm leading-6">Se guardará una copia previa cifrada en el servidor con la contraseña indicada para esta restauración. Podrás descargarla y restaurarla para recuperar los datos anteriores. Si ya faltan originales o están dañados, esa copia conservará sus metadatos y señalará que no están disponibles.</p>
      <p class="text-sm leading-6">Las cuentas, roles y capacidades actuales se conservan. Los accesos compartidos deben aparecer en la copia y seguir vigentes; los demás deberán compartirse de nuevo.</p>
      <p class="text-sm leading-6 text-muted-foreground">Durante la operación se pausarán las demás solicitudes. Espera a que termine y evita cerrar esta pantalla.</p>
      <p v-if="error" role="alert" class="text-sm leading-6 text-destructive">{{ error }}</p>
      <div class="flex flex-wrap gap-3"><Button variant="outline" :disabled="busy" @click="confirming = false">Cancelar</Button><Button :disabled="busy" @click="restore">{{ busy ? 'Restaurando…' : 'Confirmar restauración' }}</Button></div>
    </div>
  </ActionDialog>
</template>
