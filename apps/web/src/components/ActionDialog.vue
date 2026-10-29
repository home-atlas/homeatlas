<script setup lang="ts">
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import AppIcon from '@/components/AppIcon.vue'
const props = defineProps<{ title: string; description: string; busy?: boolean; returnFocus?: () => HTMLElement | null }>()
function restoreFocus(event: Event) {
  const target = props.returnFocus?.()
  if (target?.isConnected) { event.preventDefault(); target.focus() }
}
const emit = defineEmits<{ close: [] }>()
</script>
<template>
  <Sheet :open="true" @update:open="open => { if (!open && !busy) emit('close') }"><SheetContent :show-close-button="false" @close-auto-focus="restoreFocus" @escape-key-down="event => { if (busy) event.preventDefault() }" @pointer-down-outside="event => { if (busy) event.preventDefault() }"><SheetHeader class="flex-row items-start justify-between gap-4 border-b p-6"><div><SheetTitle class="text-xl font-semibold">{{ title }}</SheetTitle><SheetDescription class="mt-2 text-sm leading-6 text-muted-foreground">{{ description }}</SheetDescription></div><SheetClose as-child><Button variant="ghost" size="icon" aria-label="Cerrar" :disabled="busy"><AppIcon name="close" /></Button></SheetClose></SheetHeader><div class="min-h-0 flex-1 overflow-y-auto p-6"><slot /></div><footer v-if="$slots.footer" class="shrink-0 border-t bg-card px-6 py-4"><slot name="footer" /></footer></SheetContent></Sheet>
</template>
