<script setup lang="ts">
import { ref, watch } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
const props = defineProps<{ id: string; label: string; help: string; autocomplete?: string; disabled?: boolean; invalid?: boolean }>()
const value = defineModel<string>({ required: true })
const shown = ref(false), input = ref<InstanceType<typeof Input>>()
watch([value, () => props.disabled], ([current, disabled]) => { if (!current || disabled) shown.value = false })
defineExpose({ focus: () => input.value?.focus() })
</script>
<template>
  <div class="space-y-3">
    <Label :for="id" class="block text-base font-semibold text-heading">{{ label }}</Label>
    <Input :id="id" ref="input" v-model="value" :type="shown ? 'text' : 'password'" inputmode="numeric" :autocomplete="autocomplete ?? 'off'" pattern="[0-9]{6,12}" minlength="6" maxlength="12" required :disabled="disabled" :aria-invalid="invalid || undefined" :aria-describedby="id + '-help'" class="form-input form-input-lg text-xl tracking-widest" />
    <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
      <p :id="id + '-help'" class="text-base leading-7 text-muted-foreground">{{ help }}</p>
      <Button type="button" variant="outline" size="sm" :disabled="disabled" :aria-controls="id" :aria-pressed="shown" :aria-label="(shown ? 'Ocultar: ' : 'Mostrar: ') + label" @click="shown = !shown"><AppIcon :name="shown ? 'eye-off' : 'eye'" class="size-5" />{{ shown ? 'Ocultar PIN' : 'Mostrar PIN' }}</Button>
    </div>
  </div>
</template>
