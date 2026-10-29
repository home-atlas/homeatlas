<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
defineProps<{ class?: HTMLAttributes['class'] }>()
</script>
<template><nav data-slot="breadcrumb" aria-label="Ruta de navegación" :class="$props.class"><slot /></nav></template>
