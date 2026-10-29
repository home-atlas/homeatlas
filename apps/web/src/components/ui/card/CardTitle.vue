<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'

const props = defineProps<{
  class?: HTMLAttributes['class']
}>()
</script>

<template>
  <div
    data-slot="card-title"
    :class="cn('font-heading text-lg leading-7 font-semibold group-data-[size=sm]/card:text-sm', props.class)"
  >
    <slot />
  </div>
</template>
