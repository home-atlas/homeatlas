<script setup lang="ts">
import { ref, type HTMLAttributes } from 'vue'
import { useVModel } from '@vueuse/core'
import { cn } from 'cn'

const props = defineProps<{ defaultValue?: string | number; modelValue?: string | number; class?: HTMLAttributes['class'] }>()
const emits = defineEmits<{ (event: 'update:modelValue', value: string | number): void }>()
const element = ref<HTMLTextAreaElement>()
defineExpose({ focus: () => element.value?.focus(), select: () => element.value?.select() })
const modelValue = props.modelValue === undefined
  ? useVModel(props, 'modelValue', emits, { passive: true, defaultValue: props.defaultValue })
  : useVModel(props, 'modelValue', emits, { passive: false, defaultValue: props.defaultValue })
</script>

<template>
  <textarea ref="element" v-model="modelValue" data-slot="textarea" :class="cn('form-input min-w-0 aria-invalid:border-destructive', props.class)" />
</template>
