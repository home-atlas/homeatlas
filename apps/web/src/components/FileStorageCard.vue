<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ApiError, get, patch } from '@/core/api'
import { session } from '@/core/auth'
import { canManageHousehold, householdError } from '@/core/household'
import ActionDialog from '@/components/ActionDialog.vue'
import FileStorageMoveDialog from '@/components/FileStorageMoveDialog.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input, PasswordInput } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Checkbox } from '@/components/ui/checkbox'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

interface S3 { endpoint: string; region: string; bucket: string; prefix: string; pathStyle: boolean; accessKey: string; secretKey: string }
interface Settings { provider: 'local' | 's3'; revision: string; credentialsConfigured: boolean; s3: S3 }
const allowed = computed(() => session.value?.role === 'owner' && canManageHousehold(session.value))
const current = ref<Settings>(), loading = ref(true), busy = ref(false), opened = ref(false), error = ref(''), notice = ref('')
const form = reactive<Settings>({ provider: 'local', revision: '', credentialsConfigured: false, s3: { endpoint: '', region: 'us-east-1', bucket: '', prefix: '', pathStyle: true, accessKey: '', secretKey: '' } })
const moving=ref(false)
let generation = 0
const retainsCredentials = computed(() => current.value?.provider === 's3' && current.value.credentialsConfigured && ['endpoint', 'region', 'bucket', 'prefix', 'pathStyle'].every(key => form.s3[key as keyof S3] === current.value?.s3[key as keyof S3]))
function message(cause: unknown) {
  if (cause instanceof ApiError) {
    if (cause.code === 'invalid_storage_settings') return 'Revisa los datos de S3. Usa un endpoint HTTPS y completa ambas credenciales si cambias la conexión.'
    if (cause.code === 'storage_settings_changed') return 'La configuración cambió en otra sesión. Cierra el formulario y vuelve a cargarla.'
    if (cause.code === 'storage_unavailable') return 'No pudimos acceder al almacenamiento. Para S3, comprueba la conexión y los permisos de lectura, escritura y borrado del bucket.'
  }
  return householdError(cause)
}
async function load() {
  const request = ++generation
  current.value = undefined; loading.value = true; error.value = ''; notice.value = ''; opened.value = false; moving.value=false
  form.s3.accessKey = ''; form.s3.secretKey = ''
  if (!allowed.value) { loading.value = false; return }
  try { const value = await get<Settings>('/api/household/storage'); if (request === generation) current.value = value }
  catch (cause) { if (request === generation) error.value = message(cause) }
  finally { if (request === generation) loading.value = false }
}
function edit() {
  if (!current.value || busy.value) return
  Object.assign(form, { ...current.value, s3: { ...current.value.s3, accessKey: '', secretKey: '' } })
  error.value = ''; notice.value = ''; opened.value = true
}
function returnFocus() { return document.getElementById('file-storage-configure') }
function close(value: boolean) {
  if (busy.value) return
  opened.value = value
  if (!value) { form.s3.accessKey = ''; form.s3.secretKey = ''; error.value = '' }
}
async function save() {
  if (busy.value || !allowed.value) return
  const request = generation
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const saved = await patch<Settings>('/api/household/storage', { ...form, s3: { ...form.s3 } }, 45000)
    if (request !== generation) return
    current.value = saved; opened.value = false
    form.s3.accessKey = ''; form.s3.secretKey = ''
    notice.value = saved.provider === 's3' ? 'Conexión comprobada. Las nuevas subidas se guardarán en S3.' : 'Las nuevas subidas se guardarán en este servidor.'
  } catch (cause) { if (request === generation) error.value = message(cause) }
  finally { busy.value = false }
}
watch(() => `${session.value?.household.id}/${session.value?.user.id}/${allowed.value}`, load, { immediate: true })
</script>

<template>
  <Card v-if="allowed">
    <CardHeader><CardTitle>Almacenamiento de archivos</CardTitle><CardDescription>Elige el destino de las nuevas subidas. También puedes trasladar los documentos existentes con una revisión previa.</CardDescription></CardHeader>
    <CardContent class="space-y-4">
      <p v-if="loading" role="status" class="text-sm text-muted-foreground">Cargando almacenamiento…</p>
      <template v-else-if="current">
        <dl class="text-sm"><dt class="text-muted-foreground">Destino actual</dt><dd class="mt-1 font-semibold text-heading">{{ current.provider === 'local' ? 'Local · Este servidor' : `S3 · ${current.s3.bucket}` }}</dd></dl>
        <div class="flex flex-wrap gap-3"><Button id="file-storage-configure" variant="outline" @click="edit">Configurar almacenamiento</Button><Button id="file-storage-move" variant="outline" @click="moving = true">Mover documentos existentes</Button></div>
      </template>
      <Button v-else variant="outline" @click="load">Reintentar almacenamiento</Button>
      <Alert v-if="error && !opened" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <p v-if="notice" role="status" class="text-sm text-primary">{{ notice }}</p>
    </CardContent>
    <FileStorageMoveDialog v-if="moving" @close="moving = false" />
    <ActionDialog v-if="opened" title="Almacenamiento de archivos" description="Local es la opción predeterminada. Cambiar el destino no mueve los archivos existentes." :busy="busy" :return-focus="returnFocus" @close="close(false)">
      <form id="file-storage-form" class="space-y-5" :aria-busy="busy" @submit.prevent="save">
        <fieldset :disabled="busy" class="space-y-5">
          <div class="space-y-2"><Label for="storage-provider">Guardar nuevas subidas en</Label><Select v-model="form.provider" :disabled="busy"><SelectTrigger id="storage-provider"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="local">Local · Este servidor</SelectItem><SelectItem value="s3">S3 · Almacenamiento de objetos</SelectItem></SelectContent></Select></div>
          <template v-if="form.provider === 's3'">
            <div class="space-y-2"><Label for="storage-bucket">Bucket</Label><Input id="storage-bucket" v-model="form.s3.bucket" maxlength="63" required autocomplete="off" /></div>
            <div class="space-y-2"><Label for="storage-region">Región</Label><Input id="storage-region" v-model="form.s3.region" maxlength="63" required autocomplete="off" placeholder="us-east-1" /></div>
            <div class="space-y-2"><Label for="storage-endpoint">Endpoint compatible (opcional)</Label><Input id="storage-endpoint" v-model="form.s3.endpoint" type="url" maxlength="500" placeholder="https://…" autocomplete="off" /><p class="text-sm text-muted-foreground">Déjalo vacío para usar Amazon S3.</p></div>
            <div class="space-y-2"><Label for="storage-prefix">Carpeta o prefijo (opcional)</Label><Input id="storage-prefix" v-model="form.s3.prefix" maxlength="200" placeholder="homeatlas" autocomplete="off" /></div>
            <Label for="storage-path-style" class="flex min-h-11 items-center gap-3"><Checkbox id="storage-path-style" v-model="form.s3.pathStyle" :disabled="busy" />Usar bucket en la ruta (path-style)</Label>
            <div class="space-y-2"><Label for="storage-access-key">Clave de acceso</Label><PasswordInput id="storage-access-key" visibility-label="clave de acceso" v-model="form.s3.accessKey" :required="!retainsCredentials || !!form.s3.secretKey" maxlength="256" autocomplete="off" :disabled="busy" /></div>
            <div class="space-y-2"><Label for="storage-secret-key">Clave secreta</Label><PasswordInput id="storage-secret-key" visibility-label="clave secreta" v-model="form.s3.secretKey" :required="!retainsCredentials || !!form.s3.accessKey" maxlength="512" autocomplete="new-password" :disabled="busy" /></div>
            <p class="text-sm leading-6 text-muted-foreground">{{ retainsCredentials ? 'Deja ambas claves vacías para conservarlas. ' : '' }}Se guardan cifradas en el servidor. Antes de activar S3 comprobaremos lectura, escritura y borrado. Usa un bucket privado.</p>
          </template>
        </fieldset>
        <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
      </form>
      <template #footer><div class="flex flex-wrap justify-end gap-3"><Button variant="outline" :disabled="busy" @click="close(false)">Cancelar</Button><Button type="submit" form="file-storage-form" :disabled="busy">{{ busy ? 'Comprobando y guardando…' : form.provider === 's3' ? 'Probar y guardar S3' : 'Guardar almacenamiento' }}</Button></div></template>
    </ActionDialog>
  </Card>
</template>
