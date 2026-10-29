<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { TabsContent, type TabsContentProps } from 'reka-ui'
import { reactiveOmit } from '@vueuse/core'
import { cn } from 'cn'
const props = defineProps<TabsContentProps & { class?: HTMLAttributes['class'] }>()
const delegated = reactiveOmit(props, 'class')
</script>
<template><TabsContent data-slot="tabs-content" v-bind="delegated" :class="cn('mt-5 min-w-0 rounded-xl focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-offset-2 focus-visible:outline-ring', props.class)"><slot /></TabsContent></template>
