<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { TabsTrigger, type TabsTriggerProps } from 'reka-ui'
import { reactiveOmit } from '@vueuse/core'
import { cn } from 'cn'
const props = defineProps<TabsTriggerProps & { class?: HTMLAttributes['class'] }>()
const delegated = reactiveOmit(props, 'class')
</script>
<template><TabsTrigger data-slot="tabs-trigger" v-bind="delegated" :class="cn('inline-flex min-h-12 items-center justify-center gap-2 rounded-lg px-4 py-3 text-sm font-semibold whitespace-nowrap transition-colors hover:text-heading focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-offset-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 data-[state=active]:bg-card data-[state=active]:text-heading data-[state=active]:shadow-sm', props.class)"><slot /></TabsTrigger></template>
