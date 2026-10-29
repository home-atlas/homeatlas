<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { modules, refreshModules } from '@/core/state'
import { canOpenModule, type ModuleStatus } from '@/core/modules'
import { ApiError, patch } from '@/core/api'
import { refreshSession, session } from '@/core/auth'
import { canManageHousehold, householdError, permissionLabel } from '@/core/household'
import ActionDialog from '@/components/ActionDialog.vue'

const router = useRouter()
const canManage = computed(() => canManageHousehold(session.value))
const selected = ref<{ module: ModuleStatus; enabled: boolean }>(), busy = ref(false), error = ref(''), notice = ref('')
function choose(module: ModuleStatus) { selected.value = { module, enabled: !module.enabled }; error.value = ''; notice.value = '' }
async function save() {
  if (!selected.value || busy.value || !canManage.value) return
  const { module, enabled } = selected.value
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await patch<void>('/api/household/modules/' + encodeURIComponent(module.id), { enabled }, 30000)
    selected.value = undefined
    await refreshModules(router, true)
    notice.value = `${module.navigation.label} ${enabled ? 'activado' : 'desactivado'} en este hogar. Sus datos y permisos se conservan.`
  } catch (cause) {
    error.value = cause instanceof ApiError && cause.code === 'module_dependency_conflict' ? 'Revisa las dependencias: activa primero los módulos necesarios o desactiva antes los que dependen de este.'
      : cause instanceof ApiError && cause.code === 'module_unavailable' ? 'Este módulo no está disponible en la instalación. Consulta con quien administra el servidor.' : householdError(cause)
    await refreshSession().catch(() => {})
    if (!session.value) await router.replace('/login?redirect=%2Fmodules')
  } finally { busy.value = false }
}
</script>

<template>
  <section aria-labelledby="modules-title" class="space-y-8">
    <div><p class="mb-3 text-sm text-primary">Un hogar a tu medida</p><h1 id="modules-title" class="text-3xl font-semibold tracking-tight">Explorar módulos</h1><p class="mt-3 text-muted-foreground">Herramientas para {{ session?.household.name }}. La activación no cambia tus permisos de acceso.</p></div>
    <p v-if="notice" role="status" class="rounded-xl bg-secondary p-4 text-sm leading-6 text-primary">{{ notice }}</p>
    <p v-if="error && !selected" role="alert" class="rounded-xl border border-destructive/30 p-4 text-sm text-destructive">{{ error }}</p>
    <p v-if="!canManage" class="text-sm leading-6 text-muted-foreground">Solo el propietario o un administrador con permiso de gestión puede activar o desactivar módulos.</p>
    <div class="grid gap-5 sm:grid-cols-2">
      <Card v-for="module in modules" :key="module.id">
        <CardHeader>
          <div class="mb-5 flex items-center justify-between"><span class="rounded-xl bg-secondary px-4 py-3 text-xl text-primary" aria-hidden="true">▤</span><Badge :variant="module.enabled ? 'secondary' : 'outline'">{{ !module.available ? 'No disponible' : module.enabled ? 'Activo' : 'Inactivo' }}</Badge></div>
          <CardTitle>{{ module.navigation.label }}</CardTitle>
          <CardDescription>{{ module.id === 'documents' ? 'Biblioteca de documentos del hogar: manuales, facturas y archivos importantes.' : module.name }}</CardDescription>
        </CardHeader>
        <CardContent class="space-y-5">
          <p class="text-xs text-muted-foreground">Versión {{ module.version }} · Acceso sujeto a permisos</p>
          <ul class="space-y-1 text-xs leading-6 text-muted-foreground"><li v-for="capability in module.capabilities" :key="capability">{{ permissionLabel(capability) }}</li></ul>
          <p class="text-xs leading-6 text-muted-foreground">Dependencias: {{ module.dependencies.length ? module.dependencies.map(id => modules.find(m => m.id === id)?.navigation.label ?? id).join(', ') : 'ninguna' }}.</p>
          <p v-if="module.enabled && !canOpenModule(module, session?.permissions ?? [])" class="text-sm text-muted-foreground">Tu cuenta no tiene permiso para abrir este módulo.</p>
          <div class="flex flex-wrap gap-3"><Button v-if="canOpenModule(module, session?.permissions ?? [])" variant="outline" as-child><RouterLink :to="module.navigation.path">Abrir módulo <span aria-hidden="true">→</span></RouterLink></Button><Button v-if="canManage && module.available" :variant="module.enabled ? 'outline' : 'default'" :disabled="busy" @click="choose(module)">{{ module.enabled ? 'Desactivar' : 'Activar' }}</Button></div>
        </CardContent>
      </Card>
    </div>
    <Card v-if="!modules.length"><CardHeader><CardTitle>Tu hogar está listo</CardTitle><CardDescription>La configuración, los miembros y tu acceso siguen disponibles. Esta instalación funciona sin módulos adicionales.</CardDescription></CardHeader><CardContent><Button variant="outline" as-child><RouterLink to="/">Volver a Inicio</RouterLink></Button></CardContent></Card>
    <p v-else class="max-w-2xl text-sm leading-6 text-muted-foreground">Aquí aparecen los plugins instalados. Inventario y garantías llegarán en etapas posteriores.</p>
    <ActionDialog v-if="selected" :title="`${selected.enabled ? 'Activar' : 'Desactivar'} ${selected.module.navigation.label}`" :description="`En el hogar ${session?.household.name}`" :busy="busy" @close="selected = undefined"><form class="space-y-6" @submit.prevent="save"><p class="text-sm leading-7 text-muted-foreground">{{ selected.enabled ? 'El módulo volverá a estar disponible con sus datos y permisos actuales. Activarlo no concede acceso adicional a sus documentos.' : 'Se ocultará de la navegación y dejará de estar disponible en este hogar, también en la búsqueda. Los archivos, metadatos y permisos se conservan para cuando lo actives de nuevo.' }}</p><p v-if="error" role="alert" class="text-sm leading-6 text-destructive">{{ error }}</p><div class="flex flex-wrap gap-3"><Button type="button" variant="outline" :disabled="busy" @click="selected = undefined">Cancelar</Button><Button type="submit" :disabled="busy || !canManage">{{ busy ? 'Guardando…' : selected.enabled ? 'Confirmar activación' : 'Confirmar desactivación' }}</Button></div></form></ActionDialog>
  </section>
</template>
