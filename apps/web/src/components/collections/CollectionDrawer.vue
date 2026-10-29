<script setup lang="ts">
import { useCollection } from './model'
import { computed, nextTick, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from '@/components/AppIcon.vue'
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { ApiError, patch, upload } from '@/core/api'
import { session, setAuthState } from '@/core/auth'
import { errorMessage, formatSize, type Item } from './model'

const { collectionId, config, categories, validateFile, fileTypes } = useCollection()

const props = defineProps<{ document?: Item; returnFocus?: () => HTMLElement | null }>()
const emit = defineEmits<{ close: []; saved: [document: Item] }>()
const router = useRouter(), route = useRoute()
const editing = computed(() => !!props.document)
const form = reactive({ title: props.document?.title ?? '', category: props.document?.category ?? categories[categories.length - 1]!.value, description: props.document?.description ?? '', tags: props.document?.tags.join(', ') ?? '' })
const file = ref<File>(), busy = ref(false), error = ref('')
const fileError = ref(''), dragDepth = ref(0), suggestedTitle = ref('')
const fileInput = ref<InstanceType<typeof Input>>(), pickerButton = ref<InstanceType<typeof Button>>()
const dragging = computed(() => dragDepth.value > 0)
const canWrite = computed(() => session.value?.permissions.includes(collectionId + '.write'))
function focusPicker() { nextTick(() => pickerButton.value?.$el.focus()) }
function clearFile() {
  if (busy.value || !canWrite.value) return
  if (form.title === suggestedTitle.value) form.title = ''
  suggestedTitle.value = ''; file.value = undefined; fileError.value = ''; error.value = ''
  fileInput.value?.clear(); focusPicker()
}
function chooseFiles(files: File[]) {
  if (busy.value || !canWrite.value || !files.length) return
  clearFile()
  fileError.value = files.length > 1 ? 'Elige un solo archivo a la vez.' : validateFile(files[0]!)
  if (!fileError.value) {
    file.value = files[0]!
    suggestedTitle.value = file.value.name.replace(/\.[^.]+$/, '').slice(0, config.titleMaxLength)
    if (!form.title) form.title = suggestedTitle.value
  }
  focusPicker()
}
function choose(event: Event) {
  chooseFiles(Array.from((event.target as HTMLInputElement).files ?? []))
}
function fileDrag(event: DragEvent) { return Array.from(event.dataTransfer?.types ?? []).includes('Files') }
function enter(event: DragEvent) { if (!busy.value && canWrite.value && fileDrag(event)) dragDepth.value++ }
function over(event: DragEvent) {
  if (event.dataTransfer) event.dataTransfer.dropEffect = !busy.value && canWrite.value && fileDrag(event) ? 'copy' : 'none'
}
function drop(event: DragEvent) {
  dragDepth.value = 0
  chooseFiles(Array.from(event.dataTransfer?.files ?? []))
}
function close() { if (!busy.value) emit('close') }
function restoreFocus(event: Event) {
  const target = props.returnFocus?.()
  if (target?.isConnected) { event.preventDefault(); target.focus() }
}
async function save() {
  if (busy.value || !canWrite.value) return
  error.value = ''
  if (!editing.value) {
    if (!file.value) { fileError.value ||= 'Elige un archivo para continuar.'; focusPicker(); return }
    if ((fileError.value = validateFile(file.value))) return
  }
  const metadata = { title: form.title.trim(), category: form.category, description: form.description.trim(), tags: form.tags.split(',').map(t => t.trim()).filter(Boolean) }
  if (!metadata.title || metadata.tags.length > config.maxTags || metadata.tags.some(t => [...t].length > config.tagMaxLength)) { error.value = `Escribe un título y hasta ${config.maxTags} etiquetas de ${config.tagMaxLength} caracteres.`; return }
  busy.value = true
  try {
    let result: Item
    if (props.document) result = await patch<Item>('/api/' + collectionId + '/' + encodeURIComponent(props.document.id), metadata)
    else {
      const data = new FormData(); data.append('file', file.value!); data.append('metadata', JSON.stringify(metadata))
      result = await upload<Item>('/api/' + collectionId, data)
    }
    emit('saved', result)
  } catch (err) {
    error.value = errorMessage(err)
    if (err instanceof ApiError && err.status === 401) { setAuthState({ setupRequired: false, session: null }); await router.replace('/login?redirect=' + encodeURIComponent(route.fullPath)) }
  } finally { busy.value = false }
}
</script>

<template>
  <Sheet :open="true" @update:open="open => { if (!open) close() }">
      <SheetContent :show-close-button="false" class="data-[side=right]:max-w-[var(--drawer-width)]" @close-auto-focus="restoreFocus" @escape-key-down="event => { if (busy) event.preventDefault() }" @pointer-down-outside="event => { if (busy) event.preventDefault() }">
        <SheetHeader class="flex-row items-start justify-between gap-4 border-b px-6 py-6 sm:px-8">
          <div><SheetTitle class="text-xl font-semibold">{{ editing ? 'Editar metadatos' : 'Subir archivo' }}</SheetTitle><SheetDescription class="mt-2 text-sm leading-6 text-muted-foreground">{{ editing ? 'Organiza los datos sin modificar el archivo original.' : 'Guarda un original y dale un lugar en tu biblioteca.' }}</SheetDescription></div>
          <SheetClose as-child><Button variant="ghost" size="icon" aria-label="Cerrar formulario" :disabled="busy"><AppIcon name="close" /></Button></SheetClose>
        </SheetHeader>
        <form class="flex min-h-0 flex-1 flex-col" :aria-busy="busy" @submit.prevent="save">
          <fieldset class="min-h-0 flex-1 space-y-5 overflow-y-auto px-6 py-6 sm:px-8" :disabled="busy">
            <div v-if="!editing" class="space-y-3">
              <Label id="document-file-label" for="document-file">Archivo</Label>
              <Input id="document-file" ref="fileInput" type="file" :accept="config.mediaTypes.join(',')" class="sr-only" tabindex="-1" aria-hidden="true" :disabled="busy || !canWrite" @change="choose" />
              <Card role="group" aria-labelledby="document-file-label" :aria-disabled="busy || !canWrite" :data-dragging="dragging || undefined" class="gap-0 border-2 border-dashed py-0 shadow-none transition-colors focus-within:border-primary" :class="fileError ? 'border-destructive bg-danger-muted' : dragging ? 'border-primary bg-secondary' : file ? 'border-primary/40 bg-secondary/30' : 'border-input bg-background hover:border-primary'" @dragenter.prevent="enter" @dragover.prevent="over" @dragleave.prevent="dragDepth = Math.max(0, dragDepth - 1)" @drop.prevent.stop="drop">
                <CardContent class="p-5 sm:p-6">
                  <div v-if="file" class="flex items-start gap-3">
                    <span class="grid size-12 shrink-0 place-items-center rounded-xl border border-primary/15 bg-card text-primary"><AppIcon name="document" /></span>
                    <div class="min-w-0 flex-1"><p class="break-all text-sm font-semibold leading-6 text-heading">{{ file.name }}</p><div class="mt-2 flex flex-wrap items-center gap-2"><Badge variant="secondary">{{ file.name.split('.').pop()?.toUpperCase() }}</Badge><span class="text-xs text-muted-foreground">{{ formatSize(file.size) }}</span></div></div>
                    <Button type="button" variant="ghost" size="icon-sm" aria-label="Quitar archivo" :disabled="busy || !canWrite" @click="clearFile"><AppIcon name="close" class="size-4" /></Button>
                  </div>
                  <div v-else class="text-center"><span class="mx-auto mb-4 grid size-14 place-items-center rounded-2xl border border-primary/15 bg-card text-primary"><AppIcon name="upload" class="size-7" /></span><p class="font-semibold text-heading">{{ dragging ? 'Suelta el archivo aquí' : fileError ? 'Prueba con otro archivo' : 'Arrastra tu archivo aquí' }}</p><p class="mt-1 text-sm leading-6 text-muted-foreground">o selecciónalo desde tu dispositivo</p></div>
                  <div class="mt-4 flex flex-wrap items-center justify-center gap-3" :class="file ? 'sm:justify-between' : ''"><span v-if="file" role="status" class="flex items-center gap-1.5 text-xs font-medium text-primary"><AppIcon name="check" class="size-4" />Listo para guardar</span><Button id="document-choose-file" ref="pickerButton" type="button" variant="outline" :disabled="busy || !canWrite" :aria-describedby="fileError ? 'file-help file-error' : 'file-help'" :aria-invalid="!!fileError" @click="fileInput?.click()">{{ file ? 'Cambiar archivo' : 'Elegir archivo' }}</Button></div>
                  <p id="file-help" class="mt-4 text-center text-xs leading-5 text-muted-foreground">{{ fileTypes.join(', ') }} · Un archivo, hasta {{ formatSize(config.maxFileBytes) }}</p>
                </CardContent>
              </Card>
              <Alert v-if="fileError" id="file-error" role="alert" variant="destructive"><AlertDescription class="leading-6">{{ fileError }}</AlertDescription></Alert>
              <span v-if="dragging" role="status" class="sr-only">Suelta un archivo para seleccionarlo.</span>
            </div>
            <div class="space-y-2"><Label for="document-title">Título</Label><Input id="document-title" v-model="form.title" :maxlength="config.titleMaxLength" required placeholder="Título del archivo" /></div>
            <div class="space-y-2"><Label for="document-category">Categoría</Label><Select v-model="form.category" :disabled="busy"><SelectTrigger id="document-category"><SelectValue placeholder="Selecciona una categoría" /></SelectTrigger><SelectContent><SelectGroup><SelectItem v-for="category in categories" :key="category.value" :value="category.value">{{ category.label }}</SelectItem></SelectGroup></SelectContent></Select></div>
            <div class="space-y-2"><Label for="document-description">Descripción <span class="font-normal text-muted-foreground">(opcional)</span></Label><Textarea id="document-description" v-model="form.description" :maxlength="config.descriptionMaxLength" rows="4" placeholder="Los detalles que te ayudarán a encontrarlo." /></div>
            <div class="space-y-2"><Label for="document-tags">Etiquetas <span class="font-normal text-muted-foreground">(opcional)</span></Label><Input id="document-tags" v-model="form.tags" aria-describedby="tags-help" placeholder="etiqueta1, etiqueta2" /><p id="tags-help" class="text-xs text-muted-foreground">Hasta {{ config.maxTags }} etiquetas de {{ config.tagMaxLength }} caracteres, separadas por comas.</p></div>
            <Alert role="note"><AppIcon name="shield" /><AlertTitle>{{ editing ? 'Acceso conservado' : 'Privado · Solo tú' }}</AlertTitle><AlertDescription class="leading-6">{{ editing ? 'Editar estos datos no cambia quién tiene acceso.' : 'Solo tú y los miembros con quienes lo compartas podrán consultarlo. Las copias completas del hogar también lo incluyen.' }}</AlertDescription></Alert>
            <Alert v-if="error" role="alert" variant="destructive"><AlertDescription class="leading-6">{{ error }}</AlertDescription></Alert>
          </fieldset>
          <div class="grid grid-cols-1 gap-3 border-t px-6 py-5 sm:flex sm:flex-wrap sm:items-center sm:justify-end sm:px-8"><Button type="button" variant="outline" :disabled="busy" @click="close">Cancelar</Button><Button type="submit" :disabled="busy || !canWrite || (!editing && !file)">{{ busy ? 'Guardando…' : editing ? 'Guardar cambios' : 'Guardar archivo' }}</Button></div>
        </form>
      </SheetContent>
  </Sheet>
</template>
