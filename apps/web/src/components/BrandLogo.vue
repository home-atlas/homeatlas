<script setup lang="ts">
import mark from '@/assets/homeatlas-mark.svg'
withDefaults(defineProps<{ size?: 'default' | 'large' }>(), { size: 'default' })
</script>

<template>
  <span class="inline-flex items-center whitespace-nowrap font-heading leading-none font-extrabold tracking-tight text-heading" :class="size === 'large' ? 'gap-3 text-[1.75rem]' : 'gap-2.5 text-xl'"><img :src="mark" alt="" :width="size === 'large' ? 48 : 40" :height="size === 'large' ? 48 : 40" class="object-contain" :class="size === 'large' ? 'size-12' : 'size-10'" />HomeAtlas</span>
</template>
