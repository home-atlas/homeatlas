<script setup lang="ts">
import { PasswordInput } from '@/components/ui/input'
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import { setAuthState } from '@/core/auth'
import { backupError, maxBackupBytes, recoverInstallation } from '@/core/backups'
import ActionDialog from '@/components/ActionDialog.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

defineProps<{ disabled?: boolean }>()
const emit = defineEmits<{ busy: [value: boolean]; completed: [] }>()
const password = ref('')
const code = ref(''), selected = ref<File>(), input = ref<HTMLInputElement>()
const busy = ref(false), confirming = ref(false), error = ref('')
const result = ref<Awaited<ReturnType<typeof recoverInstallation>>>()
function selectFile(event: Event) {
  error.value = ''; selected.value = undefined
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  if (!file.size || file.size > maxBackupBytes) { error.value = 'Elige una copia completa .zip.age o ZIP de hasta 1 GiB.'; return }
  selected.value = file
}
async function recover() {
  if (busy.value || !selected.value || !code.value.trim()) return
  busy.value = true; emit('busy', true); error.value = ''
  try {
    result.value = await recoverInstallation(selected.value, code.value.trim(), password.value)
    code.value = ''; password.value = ''; selected.value = undefined; confirming.value = false
    if (input.value) input.value.value = ''
    setAuthState({ setupRequired: false, session: null })
    emit('completed')
  } catch (cause) { error.value = backupError(cause) }
  finally { busy.value = false; emit('busy', false) }
}
</script>

<template>
  <Card>
    <CardHeader><CardTitle>Recuperar tu hogar</CardTitle><CardDescription>Para una instalación nueva tras perder el servidor. Necesitas una copia completa en formato v2, v3 o v4.</CardDescription></CardHeader>
    <CardContent class="space-y-5" :aria-busy="busy">
      <template v-if="result">
        <p role="status" class="text-sm leading-6 text-primary">Hogar recuperado: {{ result.householdName }}. {{ result.members }} {{ result.members === 1 ? 'miembro recuperado' : 'miembros recuperados' }}, junto con los datos de los módulos incluidos.</p>
        <p v-if="result.unavailableOriginals" class="text-sm leading-6 text-muted-foreground">La copia declara {{ result.unavailableOriginals }} originales no disponibles. Sus metadatos se recuperaron, pero necesitas una copia íntegra para recuperar esos archivos.</p>
        <p class="text-sm leading-6 text-muted-foreground">Inicia sesión con el usuario o correo y la contraseña que tenías al crear la copia. Revisa miembros y permisos antes de volver a usar el servidor. Si olvidaste tu contraseña, puedes usar la recuperación local con acceso al servidor. Si usas recuperación por correo, vuelve a verificar tu buzón.</p>
        <Button as-child class="w-full"><RouterLink to="/login">Ir a iniciar sesión</RouterLink></Button>
      </template>
      <form v-else class="space-y-5" @submit.prevent="confirming = true">
        <p class="text-sm leading-6 text-muted-foreground">Se recuperan hogar, cuentas activas, roles, permisos y los datos de los módulos incluidos según el estado de la copia. El servidor necesita una versión compatible de cada módulo. Las sesiones, invitaciones y códigos de recuperación antiguos no se reutilizan. La configuración SMTP se realiza en el servidor nuevo.</p>
        <fieldset :disabled="busy || disabled" class="space-y-5">
          <div class="space-y-2"><label for="restore-setup-code" class="form-label">Código de alta de esta instalación nueva</label><PasswordInput id="restore-setup-code" v-model="code" autocomplete="off" autocapitalize="off" :spellcheck="false" required maxlength="64" class="form-input" aria-describedby="restore-setup-help" :disabled="busy || disabled" visibility-label="código de alta" /><p id="restore-setup-help" class="text-xs leading-5 text-muted-foreground">Consulta el archivo privado setup-code en la carpeta de datos del servidor nuevo. La recuperación solo se permite antes del alta inicial.</p></div>
          <div class="space-y-2"><label for="installation-backup-file" class="form-label">Copia completa del hogar</label><input id="installation-backup-file" ref="input" type="file" accept=".age,.zip,application/zip,application/octet-stream" class="form-file" required aria-describedby="installation-backup-help" @change="selectFile" /><p id="installation-backup-help" class="text-xs leading-5 text-muted-foreground">Copia cifrada .zip.age o ZIP antiguo de HomeAtlas v2, v3 o v4, hasta 1 GiB. Las copias v1 no contienen miembros y solo sirven para restaurar datos sobre un hogar existente.</p></div>
          <div class="space-y-2"><label for="installation-backup-password" class="form-label">Contraseña de la copia</label><PasswordInput id="installation-backup-password" v-model="password" autocomplete="off" maxlength="256" class="form-input" aria-describedby="installation-password-help" :disabled="busy || disabled" visibility-label="contraseña de la copia" /><p id="installation-password-help" class="text-xs leading-5 text-muted-foreground">Necesaria para una copia .zip.age. Déjala vacía si tu copia es un ZIP antiguo sin cifrar. Es distinta del código de alta y de la contraseña de tu cuenta. HomeAtlas no puede recuperarla si la pierdes.</p></div>
          <Button type="submit" :disabled="busy || !selected || !code.trim()" class="w-full">Revisar recuperación</Button>
        </fieldset>
      </form>
      <p v-if="error && !confirming" role="alert" class="text-sm leading-6 text-destructive">{{ error }}</p>
    </CardContent>
  </Card>
  <ActionDialog v-if="confirming" title="Recuperar en este servidor" description="Solo para una instalación vacía" :busy="busy" @close="confirming = false">
    <div class="space-y-5">
      <p class="break-words text-sm leading-6">Copia seleccionada: <strong>{{ selected?.name }}</strong></p>
      <p class="text-sm leading-6">Se descifrará y autenticará toda la copia antes de validar SQLite, miembros y originales antes de crear el hogar. Se recuperan las cuentas y sus contraseñas mediante hashes, los roles y los permisos guardados en la copia. Los cambios posteriores a esa copia no pueden recuperarse.</p>
      <p class="text-sm leading-6">Se crearán nuevas identidades de pertenencia para los miembros y se vincularán los accesos compartidos válidos de la copia. Las cuentas retiradas no se convierten en miembros. No se iniciará sesión automáticamente ni se enviarán correos.</p>
      <p class="text-sm leading-6 text-muted-foreground">Tras completarse, el código de alta quedará consumido y esta recuperación se cerrará. Revisa los accesos como propietario antes de volver a exponer el servidor.</p>
      <p v-if="error" role="alert" class="text-sm leading-6 text-destructive">{{ error }}</p>
      <div class="flex flex-wrap gap-3"><Button variant="outline" :disabled="busy" @click="confirming = false">Cancelar</Button><Button :disabled="busy" @click="recover">{{ busy ? 'Recuperando…' : 'Confirmar recuperación' }}</Button></div>
    </div>
  </ActionDialog>
</template>
