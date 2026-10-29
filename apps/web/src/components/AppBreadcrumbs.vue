<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { Breadcrumb, BreadcrumbList, BreadcrumbItem, BreadcrumbLink, BreadcrumbPage, BreadcrumbSeparator } from '@/components/ui/breadcrumb'
import { moduleForPath } from '@/core/modules'
import { navigationModules } from '@/core/state'
import { returnToSection } from '@/core/navigation'
const route = useRoute(), router = useRouter()
const crumbs = computed(() => {
  const module = moduleForPath(navigationModules.value, route.path)
  if (module) return route.path === module.navigation.path ? [{ label: module.navigation.label }] : [{ label: module.navigation.label, path: module.navigation.path }, { label: 'Detalle' }]
  return [{ label: typeof route.meta.breadcrumb === 'string' ? route.meta.breadcrumb : 'Página no disponible' }]
})
function backTo(event: MouseEvent, path: string) {
  if (event.button || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
  event.preventDefault(); returnToSection(router, path)
}
</script>
<template>
  <Breadcrumb class="mb-4"><BreadcrumbList><BreadcrumbItem><BreadcrumbLink as-child><RouterLink to="/" @click="backTo($event, '/')">Inicio</RouterLink></BreadcrumbLink></BreadcrumbItem><template v-for="(crumb, index) in crumbs" :key="index"><BreadcrumbSeparator /><BreadcrumbItem><BreadcrumbLink v-if="crumb.path" as-child><RouterLink :to="crumb.path" @click="backTo($event, crumb.path)">{{ crumb.label }}</RouterLink></BreadcrumbLink><BreadcrumbPage v-else>{{ crumb.label }}</BreadcrumbPage></BreadcrumbItem></template></BreadcrumbList></Breadcrumb>
</template>
