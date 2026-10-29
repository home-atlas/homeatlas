<script setup lang="ts">
import type { SelectTriggerProps } from 'reka-ui'

import type { HTMLAttributes } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import { reactiveOmit } from '@vueuse/core'
import { cn } from 'cn'
import { SelectIcon, SelectTrigger, useForwardProps } from 'reka-ui'

const props = withDefaults(
  defineProps<SelectTriggerProps & { class?: HTMLAttributes['class'], size?: 'sm' | 'default' }>(),
  { size: 'default' },
)

const delegatedProps = reactiveOmit(props, 'class', 'size')
const forwardedProps = useForwardProps(delegatedProps)
</script>

<template>
  <SelectTrigger
    data-slot="select-trigger"
    :data-size="size"
    v-bind="forwardedProps"
    :class="cn(
      'form-input flex w-full min-w-0 items-center justify-between gap-2 text-left [&_svg]:pointer-events-none [&_svg]:shrink-0 aria-invalid:border-destructive',
      props.class,
    )"
  >
    <slot />
    <SelectIcon as-child>
      <AppIcon name="chevron" class="text-muted-foreground size-4 pointer-events-none" />
    </SelectIcon>
  </SelectTrigger>
</template>
