<script setup lang="ts">
import { Primitive, type PrimitiveProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'
const props = withDefaults(defineProps<PrimitiveProps & { class?: HTMLAttributes['class'] }>(), { as: 'a' })
</script>
<template><Primitive data-slot="breadcrumb-link" :as="as" :as-child="asChild" :class="cn('inline-flex min-h-11 min-w-11 items-center rounded-sm px-1 font-medium transition-colors hover:text-primary focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring', props.class)"><slot /></Primitive></template>
