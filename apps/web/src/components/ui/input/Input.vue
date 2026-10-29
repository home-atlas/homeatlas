<script setup lang="ts">
import { ref, type HTMLAttributes } from 'vue'
import { useVModel } from '@vueuse/core'
import { cn } from 'cn'

const props = defineProps<{
  defaultValue?: string | number
  modelValue?: string | number
  class?: HTMLAttributes['class']
  type?: string
}>()

const emits = defineEmits<{
  (e: 'update:modelValue', payload: string | number): void
}>()

const element = ref<HTMLInputElement>()
defineExpose({
  focus: () => element.value?.focus(),
  select: () => element.value?.select(),
  click: () => element.value?.click(),
  clear: () => { modelValue.value = ''; if (element.value) element.value.value = '' },
})

// Controlled fields must emit before a submit in the same event turn.
const modelValue = props.modelValue === undefined
  ? useVModel(props, 'modelValue', emits, { passive: true, defaultValue: props.defaultValue })
  : useVModel(props, 'modelValue', emits, { passive: false, defaultValue: props.defaultValue })
</script>

<template>
  <input
    ref="element"
    v-model="modelValue"
    :type="type"
    data-slot="input"
    :class="cn(
      'min-w-0 aria-invalid:border-destructive',
      type === 'file' ? 'form-file' : 'form-input',
      props.class,
    )"
  >
</template>
