<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { permissionDescription, permissionLabel, roleDescription, roleLabel, roleSummary, rolePermissions, togglePermission } from '@/core/household'
import AppIcon from '@/components/AppIcon.vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Checkbox } from '@/components/ui/checkbox'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
const props = defineProps<{ roles: string[]; capabilities: string[]; prefix: string }>()
const role = defineModel<string>('role', { required: true })
const permissions = defineModel<string[]>('permissions', { required: true })
const customizing = ref(false), search = ref(''), expandedModule = ref<string | null>(null)
const available = computed(() => [...new Set([...rolePermissions(role.value, props.capabilities), ...rolePermissions(role.value, permissions.value)])])
const moduleID = (capability: string) => capability.slice(0, capability.lastIndexOf('.')) || capability
const moduleLabel = (id: string) => id === 'documents' ? 'Documentos' : id.replaceAll('-', ' ')
const shortLabel = (capability: string) => ({ 'documents.read': 'Consultar y descargar', 'documents.write': 'Subir y editar los propios', 'documents.share': 'Compartir los propios' }[capability] ?? permissionLabel(capability))
const normalize = (text: string) => text.normalize('NFD').replace(/[\u0300-\u036f]/g, '').toLowerCase()
const groups = computed(() => {
  const result = new Map<string, string[]>()
  for (const capability of available.value) {
    const id = moduleID(capability)
    result.set(id, [...(result.get(id) ?? []), capability])
  }
  return [...result].map(([id, capabilities]) => ({ id, capabilities, selected: capabilities.filter(capability => permissions.value.includes(capability)).length }))
})
const filteredGroups = computed(() => {
  const query = normalize(search.value.trim())
  return groups.value.map(group => ({ ...group, total: group.capabilities.length, capabilities: group.capabilities.filter(capability => normalize(moduleLabel(group.id) + ' ' + permissionLabel(capability) + ' ' + permissionDescription(capability)).includes(query)) })).filter(group => group.capabilities.length)
})
const selected = computed(() => available.value.filter(capability => permissions.value.includes(capability)))
const selectionSummary = computed(() => {
  if (!selected.value.length) return 'Sin permisos de módulos'
  if (groups.value.length === 1 && groups.value[0]?.id === 'documents' && selected.value.length <= 3) return selected.value.map(shortLabel).join(' · ')
  const count = groups.value.filter(group => group.selected).length
  return `${selected.value.length} ${selected.value.length === 1 ? 'permiso' : 'permisos'} en ${count} ${count === 1 ? 'módulo' : 'módulos'}`
})
watch(role, () => { permissions.value = rolePermissions(role.value, permissions.value); search.value = '' })
watch(customizing, open => {
  search.value = ''
  expandedModule.value = open && groups.value.length === 1 ? groups.value[0]!.id : null
})
function toggle(capability: string, checked: boolean) {
  permissions.value = togglePermission(permissions.value, capability, checked, props.capabilities)
}
</script>
<template>
  <div class="space-y-2">
    <Label :for="prefix + '-role'">Rol en el hogar</Label>
    <Select :model-value="role" :disabled="!roles.length" @update:model-value="value => role = String(value)">
      <SelectTrigger :id="prefix + '-role'" :aria-describedby="prefix + '-role-description'"><SelectValue placeholder="Selecciona un rol" /></SelectTrigger>
      <SelectContent><SelectGroup><SelectItem v-for="item in roles" :key="item" :value="item">{{ roleLabel(item) }}</SelectItem></SelectGroup></SelectContent>
    </Select>
    <p :id="prefix + '-role-description'" class="text-sm leading-6 text-muted-foreground">{{ roleSummary(role) }}</p>
  </div>

  <Collapsible v-model:open="customizing" class="rounded-xl border">
    <CollapsibleTrigger as-child>
      <Button :id="prefix + '-customize'" type="button" variant="ghost" class="group h-auto w-full justify-between gap-3 whitespace-normal p-4 text-left">
        <span class="min-w-0"><span class="block text-sm font-semibold">Permisos <Badge variant="secondary" class="ml-1">{{ selected.length }}</Badge></span><span class="mt-1 block text-sm font-normal leading-6 text-muted-foreground">{{ selectionSummary }}</span><span class="mt-1 block text-sm text-primary">{{ customizing ? 'Cerrar edición' : 'Personalizar permisos' }}</span></span>
        <AppIcon name="chevron" class="size-4 shrink-0 group-data-[state=open]:rotate-180" />
      </Button>
    </CollapsibleTrigger>
    <CollapsibleContent class="border-t">
      <div v-if="available.length > 8" class="space-y-2 border-b p-4"><Label :for="prefix + '-search'">Buscar permiso o módulo</Label><Input :id="prefix + '-search'" v-model="search" type="search" placeholder="Busca por nombre…" autocomplete="off" /></div>
      <div class="max-h-[min(24rem,50dvh)] overflow-y-auto overscroll-contain" :aria-label="'Permisos disponibles: ' + available.length" role="region" tabindex="0">
        <Collapsible v-for="group in filteredGroups" :key="group.id" :open="expandedModule === group.id || !!search.trim()" class="border-b last:border-b-0" @update:open="open => expandedModule = open ? group.id : null">
          <CollapsibleTrigger as-child><Button type="button" variant="ghost" class="group h-auto w-full justify-between gap-3 whitespace-normal rounded-none px-4 py-3 text-left"><span class="min-w-0 break-words font-semibold">{{ moduleLabel(group.id) }}</span><span class="flex shrink-0 items-center gap-2"><Badge variant="outline">{{ group.selected }} / {{ group.total }}</Badge><AppIcon name="chevron" class="size-4 group-data-[state=open]:rotate-180" /></span></Button></CollapsibleTrigger>
          <CollapsibleContent>
            <fieldset class="space-y-1 px-3 pb-3"><legend class="sr-only">Permisos de {{ moduleLabel(group.id) }}</legend>
              <Collapsible v-for="capability in group.capabilities" :key="capability" class="rounded-lg">
                <div class="flex items-center gap-1">
                  <Label :for="prefix + '-' + capability" class="form-option grid min-w-0 flex-1 px-2 py-2 font-normal"><Checkbox :id="prefix + '-' + capability" :aria-label="permissionLabel(capability)" :model-value="permissions.includes(capability)" @update:model-value="value => toggle(capability, value === true)" /><span class="min-w-0 break-words text-sm leading-6">{{ shortLabel(capability) }}<span v-if="!capabilities.includes(capability)" class="block text-xs text-muted-foreground">Solo retirar</span></span></Label>
                  <CollapsibleTrigger as-child><Button type="button" variant="ghost" size="icon-sm" :aria-label="'Información: ' + permissionLabel(capability)"><span aria-hidden="true" class="flex size-5 items-center justify-center rounded-full border text-xs">i</span></Button></CollapsibleTrigger>
                </div>
                <CollapsibleContent class="px-2 pb-3 text-sm leading-6 text-muted-foreground">{{ permissionDescription(capability) || permissionLabel(capability) }}</CollapsibleContent>
              </Collapsible>
            </fieldset>
          </CollapsibleContent>
        </Collapsible>
        <p v-if="!filteredGroups.length" class="p-4 text-sm leading-6 text-muted-foreground">{{ search.trim() ? 'No hay coincidencias. Prueba con otro nombre.' : 'No hay permisos disponibles para este rol.' }}</p>
      </div>
      <p class="border-t px-4 py-3 text-xs leading-5 text-muted-foreground">{{ ['restricted', 'guest'].includes(role) ? 'Este rol solo admite lectura.' : 'Subir o compartir incluye lectura. Quitar lectura retira esos permisos del mismo módulo.' }}</p>
    </CollapsibleContent>
  </Collapsible>

  <Alert role="note" class="border-primary/15 bg-secondary/50 py-3"><AppIcon name="shield" class="size-4 text-primary" /><AlertDescription class="text-sm leading-6">Invitar no da acceso a todos los documentos. Los privados se comparten por separado.</AlertDescription></Alert>
  <Collapsible>
    <CollapsibleTrigger as-child><Button type="button" variant="link" class="group h-auto max-w-full gap-2 whitespace-normal px-0 text-left">Sobre los roles y la privacidad<AppIcon name="chevron" class="size-4 shrink-0 group-data-[state=open]:rotate-180" /></Button></CollapsibleTrigger>
    <CollapsibleContent class="space-y-4 pt-2">
      <dl class="space-y-4"><div v-for="item in roles" :key="item"><dt class="text-sm font-semibold text-heading">{{ roleLabel(item) }}</dt><dd class="mt-1 text-sm leading-6 text-muted-foreground">{{ roleDescription(item) }}</dd></div></dl>
      <p class="text-sm leading-6 text-muted-foreground">{{ roleDescription('owner') }} Solo el propietario puede nombrar administradores. Nadie puede conceder permisos que su cuenta no tenga.</p>
      <p class="text-sm leading-6 text-muted-foreground">Sin permisos de módulos, podrá entrar al hogar sin acceder a su contenido. Ser administrador tampoco permite ver documentos privados.</p>
    </CollapsibleContent>
  </Collapsible>
</template>
