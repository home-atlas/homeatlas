<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'
const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>
<template><span data-slot="breadcrumb-page" aria-current="page" :class="cn('break-words px-1 text-heading', props.class)"><slot /></span></template>
