<script setup lang="ts">
import type { SelectValueProps } from 'reka-ui'
import { SelectValue } from 'reka-ui'

const props = defineProps<SelectValueProps>()
</script>

<template>
  <SelectValue
    data-slot="select-value"
    class="min-w-0 flex-1 text-left break-words whitespace-normal"
    v-bind="props"
  >
    <slot />
  </SelectValue>
</template>
