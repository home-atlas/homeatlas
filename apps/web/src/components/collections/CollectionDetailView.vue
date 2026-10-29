<script setup lang="ts">
import { useCollection } from './model'
import { computed, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from '@/components/ui/dropdown-menu'
import AppIcon from '@/components/AppIcon.vue'
import { ApiError, get } from '@/core/api'
import { downloadOriginal } from './download'
import { session, setAuthState } from '@/core/auth'
import CollectionDrawer from './CollectionDrawer.vue'
import SharingDialog from './SharingDialog.vue'
import { errorMessage, formatDate, formatSize, type Item } from './model'

const { collectionId } = useCollection()

const route = useRoute(), router = useRouter()
const document = ref<Item>(), busy = ref(true), error = ref(''), actionError = ref(''), downloading = ref(false), editing = ref(false)
const canRead = computed(() => !!session.value?.permissions.includes(collectionId + '.read'))
const isOwner = computed(() => !!document.value && document.value.ownerId === session.value?.user.id)
const canWrite = computed(() => isOwner.value && session.value?.permissions.includes(collectionId + '.write'))
const canShare = computed(() => isOwner.value && session.value?.permissions.includes(collectionId + '.share'))
const context = computed(() => JSON.stringify([session.value?.user.id, session.value?.household.id, session.value?.role, session.value?.permissions]))
const sharing = ref(false), shareCount = ref<number | null>(null), checkingAccess = ref(false), accessError = ref(false)
const fileType = computed(() => !document.value?.hasFile ? 'Sin archivo' : ({ 'application/pdf': 'PDF', 'image/png': 'PNG', 'image/jpeg': 'JPEG', 'text/plain': 'TXT' }[document.value.mediaType] ?? 'Archivo'))
const accessTitle = computed(() => !isOwner.value ? 'Compartido contigo' : shareCount.value === 0 ? 'Privado · Solo tú' : shareCount.value === null ? 'Acceso controlado' : 'Compartido con miembros')
const returnFocus = () => window.document.getElementById('document-detail-actions')
let request = 0, mounted = true
const current = (revision: number, account: string) => mounted && canRead.value && revision === request && context.value === account
async function expired(err: unknown) {
  if (err instanceof ApiError && err.status === 401) { setAuthState({ setupRequired: false, session: null }); await router.replace('/login?redirect=' + encodeURIComponent(route.fullPath)) }
}
async function checkAccess() {
  if (!document.value || !isOwner.value || checkingAccess.value) return
  const revision = request, account = context.value, id = document.value.id
  checkingAccess.value = true; accessError.value = false
  try {
    const shares = await get<unknown[]>('/api/' + collectionId + '/' + encodeURIComponent(id) + '/shares')
    if (current(revision, account)) shareCount.value = shares.length
  } catch (err) {
    if (current(revision, account)) { accessError.value = true; await expired(err) }
  } finally { if (current(revision, account)) checkingAccess.value = false }
}
async function load() {
  const revision = ++request, account = context.value
  document.value = undefined; error.value = ''; actionError.value = ''; busy.value = canRead.value; editing.value = false; sharing.value = false
  shareCount.value = null; checkingAccess.value = false; accessError.value = false; downloading.value = false
  if (!canRead.value) { error.value = 'No tienes permiso para consultar archivos.'; return }
  try {
    const result = await get<Item>('/api/' + collectionId + '/' + encodeURIComponent(String(route.params.id)))
    if (current(revision, account)) { document.value = result; void checkAccess() }
  } catch (err) { if (current(revision, account)) { error.value = errorMessage(err); await expired(err) } }
  finally { if (current(revision, account)) busy.value = false }
}
async function saveFile() {
  if (!canRead.value || !document.value?.hasFile || downloading.value) return
  const record = document.value, revision = request, account = context.value
  downloading.value = true; actionError.value = ''
  try { await downloadOriginal(collectionId, record, () => current(revision, account)) }
  catch (err) { if (current(revision, account)) { actionError.value = errorMessage(err); await expired(err) } }
  finally { if (current(revision, account)) downloading.value = false }
}
function saved(value: Item) {
  if (editing.value && canWrite.value && document.value?.id === value.id) { document.value = value; editing.value = false }
}
watch([() => route.params.id, context], load, { immediate: true, flush: 'sync' })
onUnmounted(() => { mounted = false; ++request })
</script>

<template>
  <section class="space-y-6" aria-labelledby="detail-title">
    <div v-if="busy" role="status" class="py-16 text-center text-muted-foreground">Cargando archivo…</div>
    <Card v-else-if="error"><CardContent class="space-y-5"><h1 id="detail-title" class="text-xl font-semibold">Itemo no disponible</h1><Alert role="alert" variant="destructive"><AlertDescription class="leading-6">{{ error }}</AlertDescription></Alert><Button variant="outline" @click="load">Reintentar</Button></CardContent></Card>
    <template v-else-if="document">
      <header class="flex flex-wrap items-start justify-between gap-5">
        <div class="min-w-0 flex-1 basis-72"><h1 id="detail-title" class="break-words text-3xl font-semibold tracking-tight">{{ document.title }}</h1><p class="mt-3 text-sm leading-6 text-muted-foreground">Añadido el {{ formatDate(document.createdAt) }}</p></div>
        <div class="flex w-full gap-2 sm:w-auto"><Button class="flex-1 sm:flex-none" :disabled="!document.hasFile || downloading" @click="saveFile"><AppIcon name="arrow" class="size-4 rotate-90" />{{ downloading ? 'Descargando…' : 'Descargar original' }}</Button>
          <DropdownMenu v-if="canWrite || canShare"><DropdownMenuTrigger as-child><Button id="document-detail-actions" variant="outline" size="icon" aria-label="Más acciones del archivo"><AppIcon name="more" /></Button></DropdownMenuTrigger><DropdownMenuContent align="end" class="max-w-[calc(100vw-2rem)]"><DropdownMenuItem v-if="canWrite" @select="editing = true"><AppIcon name="edit" />Editar metadatos</DropdownMenuItem><DropdownMenuItem v-if="canShare" @select="sharing = true"><AppIcon name="users" />Gestionar acceso</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
        </div>
      </header>
      <Alert v-if="actionError" role="alert" variant="destructive"><AlertDescription class="leading-6">{{ actionError }}</AlertDescription></Alert>
      <div class="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(16rem,20rem)]">
        <div class="min-w-0 space-y-6">
          <Card class="gap-0 py-0"><CardHeader class="border-b py-5"><CardTitle class="text-lg"><h2>Archivo original</h2></CardTitle><CardDescription>Conserva el formato con el que se subió.</CardDescription></CardHeader><CardContent class="space-y-6 py-6">
            <div class="flex items-start gap-4"><span class="grid size-14 shrink-0 place-items-center rounded-2xl bg-secondary text-primary"><AppIcon name="document" class="size-7" /></span><div class="min-w-0"><p class="break-all font-medium leading-6">{{ document.hasFile ? document.filename : 'Original no disponible' }}</p><div class="mt-2 flex flex-wrap items-center gap-2"><Badge variant="outline">{{ fileType }}</Badge><span v-if="document.hasFile" class="text-sm text-muted-foreground">{{ formatSize(document.size) }}</span></div></div></div>
            <template v-if="document.hasFile"><dl class="grid gap-4 border-t pt-5 text-sm sm:grid-cols-2"><div><dt class="text-muted-foreground">Formato del archivo</dt><dd class="mt-1 break-all leading-6">{{ document.mediaType }}</dd></div><div><dt class="text-muted-foreground">Fecha de carga</dt><dd class="mt-1 leading-6">{{ formatDate(document.createdAt) }}</dd></div></dl><p class="text-sm leading-6 text-muted-foreground">Descarga el original para abrirlo en tu dispositivo.</p></template>
            <Alert v-else><AlertDescription class="leading-6">El original no está disponible en este registro. El propietario del hogar puede recuperarlo desde una copia íntegra.</AlertDescription></Alert>
            <Collapsible v-if="document.sha256" class="border-t pt-2"><CollapsibleTrigger as-child><Button variant="ghost" class="group h-auto w-full justify-between whitespace-normal px-0 text-left">Integridad del archivo<AppIcon name="chevron" class="size-4 group-data-[state=open]:rotate-180" /></Button></CollapsibleTrigger><CollapsibleContent class="space-y-2 pb-2 pt-3"><p class="text-xs leading-5 text-muted-foreground">Huella SHA-256 del original al subirlo, para comprobar que el archivo no ha cambiado.</p><p class="break-all font-mono text-xs leading-6 text-muted-foreground">{{ document.sha256 }}</p></CollapsibleContent></Collapsible>
          </CardContent></Card>
          <Card class="gap-0 py-0"><CardHeader class="border-b py-5"><CardTitle class="text-lg"><h2>Sobre este archivo</h2></CardTitle><CardDescription>Detalles que ayudan a encontrarlo en la biblioteca.</CardDescription></CardHeader><CardContent class="space-y-6 py-6"><div><h3 class="text-sm font-semibold">Descripción</h3><p class="mt-2 whitespace-pre-wrap break-words text-sm leading-7 text-muted-foreground">{{ document.description || 'Sin descripción.' }}</p></div><div class="border-t pt-5"><h3 class="text-sm font-semibold">Etiquetas</h3><div v-if="document.tags.length" class="mt-3 flex flex-wrap gap-2"><Badge v-for="tag in document.tags" :key="tag" variant="outline" class="max-w-full break-all whitespace-normal">{{ tag }}</Badge></div><p v-else class="mt-2 text-sm text-muted-foreground">Sin etiquetas.</p></div></CardContent></Card>
        </div>
        <Card class="min-w-0 gap-0 py-0"><CardHeader class="border-b bg-secondary/40 py-5"><div class="mb-3 grid size-12 place-items-center rounded-xl bg-secondary text-primary"><AppIcon :name="isOwner && shareCount === 0 ? 'lock' : 'shield'" /></div><CardTitle class="text-lg"><h2>Quién puede verlo</h2></CardTitle><CardDescription class="font-medium text-primary">{{ accessTitle }}</CardDescription></CardHeader><CardContent class="space-y-5 py-6">
          <p v-if="checkingAccess" role="status" class="text-sm text-muted-foreground">Consultando acceso…</p>
          <div v-else-if="accessError" class="space-y-2"><p role="status" class="text-sm leading-6 text-muted-foreground">No pudimos consultar con quién se comparte.</p><Button variant="outline" class="w-full" @click="checkAccess">Reintentar consulta</Button></div>
          <p v-else-if="isOwner && shareCount !== null" class="text-sm leading-6 text-muted-foreground">{{ shareCount === 0 ? 'Aún no lo compartes con otros miembros del hogar.' : 'Lo compartes con ' + shareCount + (shareCount === 1 ? ' miembro del hogar.' : ' miembros del hogar.') }}</p>
          <dl class="space-y-4 text-sm"><div><dt class="text-muted-foreground">Propietario del archivo</dt><dd class="mt-1 break-words font-medium leading-6">{{ document.ownerName || (isOwner ? session?.user.name : 'Propietario del archivo') }}</dd></div><div><dt class="text-muted-foreground">Hogar</dt><dd class="mt-1 break-words leading-6">{{ session?.household.name }}</dd></div></dl>
          <p class="border-t pt-5 text-sm leading-6 text-muted-foreground">{{ isOwner ? 'Compartir permite leer y descargar este archivo, sin editarlo ni volver a compartirlo.' : 'Puedes leer y descargar este archivo. El propietario puede retirar tu acceso.' }}</p>
          <Collapsible><CollapsibleTrigger as-child><Button variant="ghost" class="group h-auto w-full justify-between whitespace-normal px-0 text-left text-sm">Acceso y copias del hogar<AppIcon name="chevron" class="size-4 shrink-0 group-data-[state=open]:rotate-180" /></Button></CollapsibleTrigger><CollapsibleContent class="space-y-3 pt-3 text-xs leading-6 text-muted-foreground"><p>Ser miembro del hogar no da acceso a todos los archivos. También se necesita permiso de lectura y que el propietario comparta cada archivo.</p><p>Las copias del hogar incluyen este archivo. Puede gestionarlas el propietario del hogar con permiso de respaldo.</p></CollapsibleContent></Collapsible>
        </CardContent></Card>
      </div>
      <SharingDialog v-if="sharing" :id="document.id" :return-focus="returnFocus" @close="sharing = false" @changed="shareCount = $event" />
      <CollectionDrawer v-if="editing" :document="document" :return-focus="returnFocus" @close="editing = false" @saved="saved" />
    </template>
  </section>
</template>
