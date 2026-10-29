<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'
const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>
<template><ol data-slot="breadcrumb-list" :class="cn('flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground', props.class)"><slot /></ol></template>
