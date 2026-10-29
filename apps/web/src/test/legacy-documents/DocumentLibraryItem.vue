<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { session } from '@/core/auth'
import AppIcon from '@/components/AppIcon.vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from '@/components/ui/dropdown-menu'
import { categoryLabel, formatDate, formatSize, type Document } from './model'

const props = defineProps<{ document: Document; view: 'cards' | 'list'; downloading: boolean; downloadDisabled: boolean }>()
const emit = defineEmits<{ edit: [document: Document]; share: [document: Document]; download: [document: Document] }>()
const own = computed(() => props.document.ownerId === session.value?.user.id)
const canEdit = computed(() => own.value && session.value?.permissions.includes('documents.write'))
const canShare = computed(() => own.value && session.value?.permissions.includes('documents.share'))
const path = computed(() => '/documents/' + encodeURIComponent(props.document.id))
const type = computed(() => !props.document.hasFile ? 'Sin archivo' : ({ 'application/pdf': 'PDF', 'image/png': 'PNG', 'image/jpeg': 'JPEG', 'text/plain': 'TXT' }[props.document.mediaType] ?? 'Archivo'))
</script>

<template>
  <Card class="relative gap-0 py-0 shadow-none" :class="view === 'cards' ? 'h-full transition-colors hover:border-primary/40' : 'rounded-none border-0 bg-transparent'">
    <CardContent :class="view === 'cards' ? 'flex h-full flex-col gap-4 p-5' : 'flex items-start gap-3 px-4 py-5 sm:gap-4 sm:px-6'">
      <div class="flex shrink-0 items-center gap-3" :class="view === 'cards' ? 'min-h-12 pr-12' : ''"><span class="grid size-12 shrink-0 place-items-center rounded-xl bg-secondary text-primary"><AppIcon name="document" /></span><Badge v-if="view === 'cards'" variant="outline">{{ type }}</Badge></div>
      <div class="min-w-0 flex-1" :class="view === 'cards' ? 'flex flex-col gap-3' : ''">
        <div><h3 class="break-words font-semibold leading-6"><RouterLink :to="path" :title="document.title" class="rounded-sm text-heading hover:text-primary focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">{{ document.title }}</RouterLink></h3><p v-if="view === 'cards'" class="mt-2 line-clamp-2 break-words text-sm leading-6 text-muted-foreground">{{ document.description || 'Sin descripción' }}</p></div>
        <div class="flex flex-wrap items-center gap-2" :class="view === 'list' ? 'mt-2' : ''"><Badge variant="secondary">{{ categoryLabel(document.category) }}</Badge><Badge v-if="!own" variant="outline" class="max-w-full whitespace-normal">Compartido contigo</Badge><span v-else class="text-xs text-muted-foreground">Tu documento</span></div>
        <div v-if="document.tags.length" class="flex flex-wrap gap-1.5" :class="view === 'list' ? 'mt-2' : ''"><Badge v-for="tag in document.tags.slice(0, 2)" :key="tag" variant="outline" class="max-w-full break-all whitespace-normal">{{ tag }}</Badge><Badge v-if="document.tags.length > 2" variant="outline" :aria-label="(document.tags.length - 2) + (document.tags.length === 3 ? ' etiqueta más' : ' etiquetas más')">+{{ document.tags.length - 2 }}</Badge></div>
        <p class="text-xs leading-6 text-muted-foreground" :class="view === 'cards' ? 'mt-auto border-t pt-3' : 'mt-2'">{{ view === 'list' ? type + ' · ' : '' }}{{ document.hasFile ? formatSize(document.size) : 'Sin archivo adjunto' }} · {{ formatDate(document.createdAt) }}</p>
      </div>
      <div :class="view === 'cards' ? 'absolute right-3 top-3' : 'shrink-0'">
        <DropdownMenu><DropdownMenuTrigger as-child><Button :id="'document-actions-' + view + '-' + document.id" type="button" variant="ghost" size="icon" :aria-label="'Acciones de ' + document.title"><AppIcon name="more" /></Button></DropdownMenuTrigger>
          <DropdownMenuContent align="end" class="max-w-[calc(100vw-2rem)]">
            <DropdownMenuItem as-child><RouterLink :to="path"><AppIcon name="book" />Ver detalle</RouterLink></DropdownMenuItem>
            <DropdownMenuItem :disabled="!document.hasFile || downloadDisabled" @select="emit('download', document)"><AppIcon name="arrow" class="rotate-90" />{{ downloading ? 'Descargando…' : 'Descargar original' }}</DropdownMenuItem>
            <DropdownMenuSeparator v-if="canEdit || canShare" />
            <DropdownMenuItem v-if="canEdit" @select="emit('edit', document)"><AppIcon name="edit" />Editar metadatos</DropdownMenuItem>
            <DropdownMenuItem v-if="canShare" @select="emit('share', document)"><AppIcon name="users" />Gestionar acceso</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </CardContent>
  </Card>
</template>
