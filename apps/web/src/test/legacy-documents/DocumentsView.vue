<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, get } from '@/core/api'
import { session, setAuthState } from '@/core/auth'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import AppIcon from '@/components/AppIcon.vue'
import DocumentDrawer from './DocumentDrawer.vue'
import DocumentLibraryItem from './DocumentLibraryItem.vue'
import SharingDialog from './SharingDialog.vue'
import { downloadOriginal } from './download'
import { categories, errorMessage, type Document } from './model'

const views = ['cards', 'list'] as const
const route = useRoute()
const view = computed({ get: () => route.query.view === 'list' ? 'list' as const : 'cards' as const, set: value => { void router.replace({ query: { ...route.query, view: value === 'list' ? 'list' : undefined } }) } })
const documents = ref<Document[]>([]), busy = ref(true), error = ref(''), showUpload = ref(false)
const query = ref(''), category = ref(''), offset = ref(0), actionError = ref(''), downloadingId = ref('')
const applied = ref({ q: '', category: '' })
const selected = ref<Document>(), action = ref<'edit' | 'share'>('edit'), focusId = ref('')
const returnFocus = () => document.getElementById(focusId.value) ?? document.getElementById('document-query')
const router = useRouter()
const canRead = computed(() => !!session.value?.permissions.includes('documents.read'))
const canWrite = computed(() => canRead.value && session.value?.permissions.includes('documents.write'))
const context = computed(() => JSON.stringify([session.value?.user.id, session.value?.household.id, session.value?.role, session.value?.permissions]))
let request = 0, mounted = true
const current = (account: string) => mounted && canRead.value && context.value === account
const own = (record: Document) => record.ownerId === session.value?.user.id
async function expired(err: unknown) {
  if (err instanceof ApiError && err.status === 401) {
    setAuthState({ setupRequired: false, session: null }); await router.replace('/login?redirect=' + encodeURIComponent(route.fullPath))
  }
}
async function filter() {
  const next = { ...route.query, q: query.value.trim() || undefined, category: category.value || undefined, offset: undefined }
  if (query.value.trim() === applied.value.q && category.value === applied.value.category && offset.value === 0) await load()
  else await router.push({ query: next })
}
async function load() {
  if (!canRead.value) { busy.value = false; return }
  const revision = ++request, account = context.value
  busy.value = true; error.value = ''; documents.value = []
  try {
    const params = new URLSearchParams({ ...applied.value, offset: String(offset.value) })
    const records = await get<Document[]>('/api/documents?' + params)
    if (revision === request && current(account)) documents.value = records
  } catch (err) {
    if (revision !== request || !current(account)) return
    error.value = errorMessage(err); await expired(err)
  } finally { if (revision === request && current(account)) busy.value = false }
}
function page(delta: number) { void router.push({ query: { ...route.query, offset: String(Math.max(0, offset.value + delta)) } }) }
function begin(record: Document, kind: 'edit' | 'share') {
  if (!canRead.value || !own(record) || !session.value?.permissions.includes(kind === 'edit' ? 'documents.write' : 'documents.share')) return
  focusId.value = 'document-actions-' + view.value + '-' + record.id
  action.value = kind; selected.value = record
}
async function saved(record: Document) {
  if (!mounted || !showUpload.value || !canWrite.value || !own(record)) return
  showUpload.value = false; await router.push('/documents/' + encodeURIComponent(record.id))
}
async function edited(record: Document) {
  if (!mounted || action.value !== 'edit' || selected.value?.id !== record.id || !canWrite.value || !own(record)) return
  selected.value = undefined; await load()
}
async function saveFile(record: Document) {
  if (!canRead.value || !record.hasFile || downloadingId.value) return
  const account = context.value
  downloadingId.value = record.id; actionError.value = ''
  try { await downloadOriginal(record, () => current(account)) }
  catch (err) { if (current(account)) { actionError.value = errorMessage(err); await expired(err) } }
  finally { if (current(account)) downloadingId.value = '' }
}
watch([context, () => route.query.q, () => route.query.category, () => route.query.offset], () => {
  query.value = typeof route.query.q === 'string' ? route.query.q.slice(0, 200) : ''
  category.value = typeof route.query.category === 'string' && categories.some(item => item.value === route.query.category) ? route.query.category : ''
  const pageOffset = typeof route.query.offset === 'string' ? Number(route.query.offset) : 0
  offset.value = Number.isSafeInteger(pageOffset) && pageOffset >= 0 && pageOffset <= 1_000_000 ? pageOffset : 0
  applied.value = { q: query.value, category: category.value }
  ++request; documents.value = []; selected.value = undefined; showUpload.value = false
  error.value = ''; actionError.value = ''; downloadingId.value = ''
  void load()
}, { immediate: true, flush: 'sync' })
onUnmounted(() => { mounted = false; ++request })
</script>

<template>
  <section aria-labelledby="documents-title" class="space-y-7">
    <div class="flex flex-wrap items-end justify-between gap-5"><div><p class="mb-3 text-sm text-primary">Todo lo importante, localizable</p><h1 id="documents-title" class="text-3xl font-semibold tracking-tight">Mis documentos</h1><p class="mt-3 max-w-xl text-sm leading-6 text-muted-foreground">Los originales de tu hogar, con sus datos en orden y acceso privado.</p></div><Button v-if="canWrite" @click="showUpload = true"><AppIcon name="plus" />Subir documento</Button></div>
    <Card><CardContent>
      <form class="grid items-end gap-4 md:grid-cols-[minmax(0,1fr)_minmax(12rem,14rem)_auto]" role="search" aria-label="Buscar documentos" @submit.prevent="filter">
        <div class="min-w-0 space-y-2"><Label for="document-query">Buscar documentos</Label><div class="relative"><AppIcon name="search" class="pointer-events-none absolute left-3 top-3 size-6 text-muted-foreground" /><Input id="document-query" v-model="query" class="pl-11" maxlength="200" placeholder="Título, descripción o etiqueta…" type="search" /></div></div>
        <div class="min-w-0 space-y-2"><Label for="category-filter">Categoría</Label><Select :model-value="category || 'all'" @update:model-value="value => category = value === 'all' ? '' : String(value)"><SelectTrigger id="category-filter"><SelectValue placeholder="Todas las categorías" /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="all">Todas las categorías</SelectItem><SelectItem v-for="item in categories" :key="item.value" :value="item.value">{{ item.label }}</SelectItem></SelectGroup></SelectContent></Select></div>
        <Button type="submit" variant="outline" :disabled="busy">Buscar</Button>
      </form>
    </CardContent></Card>
    <Alert v-if="actionError" role="alert" variant="destructive"><AlertDescription class="leading-6">{{ actionError }}</AlertDescription></Alert>
    <p v-if="downloadingId" role="status" class="text-sm text-muted-foreground">Preparando la descarga del documento…</p>
    <Tabs v-model="view" class="min-w-0">
      <Card class="gap-0 py-0">
        <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-4 border-b py-4"><div class="flex flex-wrap items-center gap-3"><CardTitle class="text-lg"><h2>Tu biblioteca</h2></CardTitle><Badge v-if="!busy && !error" variant="secondary">{{ documents.length }} en esta página</Badge></div><TabsList aria-label="Vista de la biblioteca"><TabsTrigger value="cards"><AppIcon name="grid" class="size-4" />Tarjetas</TabsTrigger><TabsTrigger value="list"><AppIcon name="list" class="size-4" />Lista</TabsTrigger></TabsList></CardHeader>
        <TabsContent v-for="mode in views" :key="mode" :value="mode" class="mt-0 rounded-none">
          <CardContent class="p-0" aria-live="polite" :aria-busy="busy">
            <div v-if="busy" class="grid min-h-64 place-items-center p-8 text-sm text-muted-foreground">Cargando tus documentos…</div>
            <div v-else-if="error" class="flex min-h-64 flex-col items-center justify-center gap-5 p-8 text-center"><Alert role="alert" variant="destructive" class="max-w-md"><AlertDescription class="leading-6">{{ error }}</AlertDescription></Alert><Button variant="outline" @click="load()">Reintentar</Button></div>
            <div v-else-if="documents.length === 0" class="flex min-h-72 flex-col items-center justify-center p-8 text-center"><span class="mb-5 grid size-14 place-items-center rounded-2xl bg-secondary text-primary"><AppIcon name="document" class="size-7" /></span><h2 class="font-semibold">{{ applied.q || applied.category || offset ? 'No hay resultados en esta página' : canWrite ? 'Un lugar para tus primeros documentos' : 'Todavía no tienes documentos disponibles' }}</h2><p class="mt-3 max-w-md text-sm leading-6 text-muted-foreground">{{ applied.q || applied.category || offset ? 'Prueba otra búsqueda, categoría o vuelve a la página anterior.' : canWrite ? 'Empieza con un manual, un recibo o un contrato. El original se guarda en tu servidor.' : 'Aquí aparecerán los documentos que otros miembros compartan contigo.' }}</p><Button v-if="canWrite && !applied.q && !applied.category && !offset" class="mt-6" @click="showUpload = true">Subir mi primer documento</Button></div>
            <ul v-else :aria-label="mode === 'cards' ? 'Documentos en tarjetas' : 'Documentos en lista'" :class="mode === 'cards' ? 'grid grid-cols-1 gap-4 p-4 sm:grid-cols-2 sm:p-5 xl:grid-cols-3' : 'divide-y'"><li v-for="document in documents" :key="document.id" class="min-w-0"><DocumentLibraryItem :document="document" :view="mode" :downloading="downloadingId === document.id" :download-disabled="!!downloadingId" @edit="begin($event, 'edit')" @share="begin($event, 'share')" @download="saveFile" /></li></ul>
          </CardContent>
        </TabsContent>
      </Card>
    </Tabs>
    <div v-if="!busy && (offset > 0 || documents.length === 50)" class="flex items-center justify-between gap-4"><Button variant="outline" :disabled="offset === 0" @click="page(-50)">Anterior</Button><span class="text-xs text-muted-foreground">Página {{ offset / 50 + 1 }}</span><Button variant="outline" :disabled="documents.length < 50" @click="page(50)">Siguiente</Button></div>
    <p class="text-xs leading-5 text-muted-foreground">PDF, PNG, JPEG y TXT · Hasta 20 MiB por archivo · Almacenamiento en tu servidor</p>
    <DocumentDrawer v-if="showUpload" @close="showUpload = false" @saved="saved" />
    <DocumentDrawer v-if="selected && action === 'edit'" :document="selected" :return-focus="returnFocus" @close="selected = undefined" @saved="edited" />
    <SharingDialog v-if="selected && action === 'share'" :id="selected.id" :return-focus="returnFocus" @close="selected = undefined" />
  </section>
</template>
