<script setup lang="ts">
import type { SelectItemProps } from 'reka-ui'

import type { HTMLAttributes } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import { reactiveOmit } from '@vueuse/core'
import { cn } from 'cn'
import {
  SelectItem,
  SelectItemIndicator,
  SelectItemText,
  useForwardProps,
} from 'reka-ui'

const props = defineProps<SelectItemProps & { class?: HTMLAttributes['class'] }>()

const delegatedProps = reactiveOmit(props, 'class')

const forwardedProps = useForwardProps(delegatedProps)
</script>

<template>
  <SelectItem
    data-slot="select-item"
    v-bind="forwardedProps"
    :class="
      cn(
        'relative flex min-h-11 w-full cursor-default items-center gap-2 rounded-lg py-2.5 pr-9 pl-3 text-sm leading-6 outline-none select-none focus:bg-secondary focus:text-primary data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none',
      props.class,
      )
    "
  >
    <span class="pointer-events-none absolute right-2 flex size-4 items-center justify-center">
      <SelectItemIndicator>
        <slot name="indicator-icon">
          <AppIcon name="check" class="pointer-events-none" />
        </slot>
      </SelectItemIndicator>
    </span>

    <SelectItemText class="min-w-0 break-words whitespace-normal">
      <slot />
    </SelectItemText>
  </SelectItem>
</template>
