<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { TabsList, type TabsListProps } from 'reka-ui'
import { reactiveOmit } from '@vueuse/core'
import { cn } from 'cn'
const props = defineProps<TabsListProps & { class?: HTMLAttributes['class'] }>()
const delegated = reactiveOmit(props, 'class')
</script>
<template><TabsList data-slot="tabs-list" v-bind="delegated" :class="cn('inline-flex min-h-14 max-w-full items-center gap-1 rounded-xl bg-muted p-1 text-muted-foreground', props.class)"><slot /></TabsList></template>
