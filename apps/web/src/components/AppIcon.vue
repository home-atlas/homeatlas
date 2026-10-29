<script setup lang="ts">
export type IconName = 'home' | 'search' | 'users' | 'document' | 'grid' | 'settings' | 'arrow' | 'chevron' | 'menu' | 'close' | 'shield' | 'sun' | 'moon' | 'folder' | 'book' | 'lock' | 'plus' | 'logout' | 'check' | 'upload' | 'list' | 'more' | 'edit' | 'eye' | 'eye-off'
withDefaults(defineProps<{ name: IconName }>(), { name: 'home' })
const paths: Record<IconName, string[]> = {
  eye: ['M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z', 'M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z'],
  'eye-off': ['M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z', 'M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z', 'm3 3 18 18'],
  list: ['M8 6h13M8 12h13M8 18h13', 'M3 6h.01M3 12h.01M3 18h.01'],
  more: ['M5 11a1 1 0 1 0 0 2 1 1 0 0 0 0-2', 'M12 11a1 1 0 1 0 0 2 1 1 0 0 0 0-2', 'M19 11a1 1 0 1 0 0 2 1 1 0 0 0 0-2'],
  edit: ['m16 3 5 5-12 12-6 1 1-6Z', 'm14 5 5 5'],
  upload: ['M12 16V3', 'm7 8 5-5 5 5', 'M3 15v4a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-4'],
  check: ['m5 12 4 4L19 6'],
  home: ['m3 10 9-7 9 7v10a1 1 0 0 1-1 1h-5v-7H9v7H4a1 1 0 0 1-1-1Z'],
  search: ['M21 21 16.5 16.5', 'M18 10.5a7.5 7.5 0 1 1-15 0 7.5 7.5 0 0 1 15 0Z'],
  users: ['M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2', 'M15 3a4 4 0 0 1 0 8', 'M22 21v-2a4 4 0 0 0-3-3.87', 'M13 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z'],
  document: ['M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z', 'M14 2v6h6', 'M8 13h8', 'M8 17h6'],
  grid: ['M3 3h7v7H3Z', 'M14 3h7v7h-7Z', 'M3 14h7v7H3Z', 'M14 14h7v7h-7Z'],
  settings: ['m9 3-1 3-3 1-2 4 2 2v3l3 3h3l2 2 4-2 1-3 3-1v-5l-3-1-1-3-4-2-2 2Z', 'M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z'],
  arrow: ['M5 12h14', 'm12 5 7 7-7 7'], chevron: ['m6 9 6 6 6-6'], menu: ['M3 6h18', 'M3 12h18', 'M3 18h18'], close: ['m6 6 12 12', 'M6 18 18 6'],
  shield: ['m12 3 8 4v5c0 5-8 9-8 9s-8-4-8-9V7Z', 'm8 12 3 3 5-6'],
  sun: ['M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z', 'M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M5 19l1.5-1.5M17.5 6.5 19 5'],
  moon: ['M21 13a9 9 0 0 1-10-10 9 9 0 1 0 10 10Z'],
  folder: ['M3 7V5a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z'],
  book: ['M12 7C8 4 5 4 2 5v15c3-1 6-1 10 2 4-3 7-3 10-2V5c-3-1-6-1-10 2Z', 'M12 7v15'],
  lock: ['M5 10h14v11H5Z', 'M8 10V6a4 4 0 0 1 8 0v4', 'M12 14v3'], plus: ['M12 5v14M5 12h14'], logout: ['M9 3H4v18h5', 'M9 12h12', 'm17 8 4 4-4 4'],
}
</script>
<template><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="size-6 shrink-0"><path v-for="(path, index) in paths[name]" :key="index" :d="path" /></svg></template>
