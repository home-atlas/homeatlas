<script setup lang="ts">
import { watch, type HTMLAttributes } from 'vue'
import Input from './Input.vue'
import { Button } from '@/components/ui/button'
import AppIcon from '@/components/AppIcon.vue'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{
  id: string
  disabled?: boolean
  readonly?: boolean
  visibilityLabel?: string
  class?: HTMLAttributes['class']
}>(), { visibilityLabel: 'contraseña' })
const value = defineModel<string>({ required: true })
const revealed = defineModel<boolean>('revealed', { default: false })
watch([value, () => props.disabled, () => props.readonly], ([current, disabled, readonly]) => {
  if (!current || disabled || readonly) revealed.value = false
})
</script>

<template>
  <div class="relative min-w-0">
    <Input v-bind="$attrs" :id="id" v-model="value" :type="revealed ? 'text' : 'password'" :disabled="disabled" :readonly="readonly" :class="['pr-14', props.class]" />
    <Button type="button" variant="ghost" size="icon-sm" :disabled="disabled || readonly" :aria-label="(revealed ? 'Ocultar ' : 'Mostrar ') + visibilityLabel" :aria-pressed="revealed" :aria-controls="id" class="absolute top-1/2 right-1 -translate-y-1/2 text-muted-foreground" @click="revealed = !revealed">
      <AppIcon :name="revealed ? 'eye-off' : 'eye'" class="size-5" />
    </Button>
  </div>
</template>
