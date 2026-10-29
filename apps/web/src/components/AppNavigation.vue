<script setup lang="ts">
import { nextTick } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import BrandLogo from './BrandLogo.vue'
import AppIcon from './AppIcon.vue'
import { navigationModules } from '@/core/state'
import homePhoto from '@/assets/login-home.jpg'
const route = useRoute()
const emit = defineEmits<{ navigate: []; search: [] }>()
async function focusSearch(event: MouseEvent) {
  if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey || event.button !== 0) return
  emit('search')
  await nextTick()
  document.getElementById('home-query')?.focus()
}
</script>
<template>
  <div class="sidebar-content flex min-h-full flex-col">
    <RouterLink to="/" aria-label="HomeAtlas, inicio" class="sidebar-brand mx-1 flex min-h-20 items-center px-2" @click="$emit('navigate')"><BrandLogo /></RouterLink>
    <nav aria-label="Principal" class="sidebar-navigation space-y-0.5" @click="$emit('navigate')">
      <RouterLink to="/" class="nav-item gap-3" :class="route.path === '/' && !route.hash ? 'nav-active' : ''"><AppIcon name="home" />Inicio</RouterLink>
      <RouterLink to="/#home-search" class="nav-item gap-3" @click="focusSearch" :class="route.path === '/' && route.hash === '#home-search' ? 'nav-active' : ''"><AppIcon name="search" />Buscar</RouterLink>
      <RouterLink to="/family" class="nav-item gap-3" active-class="nav-active"><AppIcon name="users" />Familia</RouterLink>
      <div class="sidebar-separator mx-3 border-t" />
      <RouterLink to="/modules" class="sidebar-module-label" aria-label="Ver mis módulos">Mis módulos</RouterLink>
      <RouterLink v-for="module in navigationModules" :key="module.id" :to="module.navigation.path" class="nav-item gap-3" active-class="nav-active"><AppIcon :name="module.collection ? 'document' : 'grid'" />{{ module.navigation.label }}</RouterLink>
      <div class="sidebar-separator mx-3 border-t" />
      <RouterLink to="/settings" class="nav-item gap-3" active-class="nav-active"><AppIcon name="settings" />Ajustes</RouterLink>
    </nav>
    <RouterLink to="/" class="home-sidebar-note group relative mt-auto hidden overflow-hidden rounded-xl lg:block" aria-label="Volver al inicio de tu hogar" @click="$emit('navigate')">
      <img :src="homePhoto" alt="" class="absolute inset-0 size-full object-cover object-bottom" />
      <div class="absolute inset-0 bg-linear-to-b from-card via-card/50 to-transparent" />
      <div class="relative p-4"><p class="font-heading text-lg leading-tight font-bold text-heading">Un hogar<br />más simple,<br /><span class="text-primary">siempre a mano.</span></p><span class="mt-3 inline-flex size-11 items-center justify-center rounded-full border bg-card transition-transform group-hover:translate-x-1"><AppIcon name="arrow" class="size-5" /></span></div>
    </RouterLink>
  </div>
</template>
