<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ApiError, get, post } from '@/core/api'
import { session } from '@/core/auth'
import { modules } from '@/core/state'
import type { PluginManifest, PluginView as View } from '@/core/plugins'
import AppIcon from '@/components/AppIcon.vue'
import { Badge } from '@/components/ui/badge'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'

const router = useRouter()
const props = defineProps<{ pluginId: string; viewId?: string }>()
const manifest = ref<PluginManifest>(), input = ref<Record<string, string | number>>({})
const loading = ref(false), busy = ref(false), error = ref(''), result = ref('')
let revision = 0
const active = computed(() => modules.value.some(m => m.id === props.pluginId && m.enabled && m.available))
const canRead = computed(() => active.value && !!session.value?.permissions.includes(props.pluginId + '.read'))
const view = computed(() => manifest.value?.views.find(v => v.id === props.viewId) ?? (!props.viewId ? manifest.value?.views[0] : undefined))
const canRun = computed(() => canRead.value && !!session.value?.permissions.includes(props.pluginId + '.' + (view.value?.permission ?? (view.value?.readOnly ? 'read' : 'write'))))
const context = computed(() => JSON.stringify([props.pluginId, props.viewId, session.value?.user.id, session.value?.household.id, session.value?.permissions, canRead.value]))
function actionLabel(item: View) { return item.permission === 'share' ? 'Accesos' : item.readOnly ? 'Consulta' : 'Edición' }
function actionIcon(item: View) { return item.permission === 'share' ? 'users' : item.readOnly ? 'book' : 'edit' }
function message(e: unknown) {
  if (e instanceof ApiError && e.code === 'plugin_busy') return 'Hay otra acción en curso. Espera un momento e inténtalo de nuevo.'
  if (e instanceof ApiError && e.status === 403) return 'No tienes permiso para ejecutar esta acción.'
  return 'No pudimos completar la acción del módulo. Inténtalo de nuevo.'
}
async function load() {
  const started = ++revision
  manifest.value = undefined; input.value = {}; result.value = ''; error.value = ''; busy.value = false; loading.value = false
  if (!canRead.value) return
  loading.value = true
  try { const next = await get<PluginManifest>('/api/' + props.pluginId); if (started === revision) manifest.value = next }
  catch (e) { if (started === revision) error.value = message(e) }
  finally { if (started === revision) loading.value = false }
}
async function run() {
  if (!view.value || !canRun.value || busy.value) return
  const started = revision
  busy.value = true; error.value = ''; result.value = ''
  try {
    const values = Object.fromEntries(Object.entries(input.value).map(([key, value]) => [key, String(value)]))
    const next = await post<{ text: string }>('/api/' + props.pluginId + '/actions/' + view.value.id, values, 10000)
    if (started === revision) result.value = next.text
  } catch (e) { if (started === revision) error.value = message(e) }
  finally { if (started === revision) busy.value = false }
}
watch(context, load, { immediate: true })
onUnmounted(() => { revision++ })
</script>

<template>
  <section class="space-y-6" aria-labelledby="plugin-title">
    <header class="flex flex-wrap items-center justify-between gap-5 rounded-3xl border border-border bg-card p-6 sm:p-8">
      <div class="flex min-w-0 items-center gap-4">
        <span class="flex size-14 shrink-0 items-center justify-center rounded-2xl bg-secondary text-primary"><AppIcon :name="manifest?.storage ? 'book' : 'grid'" class="size-7" /></span>
        <div class="min-w-0 space-y-2">
          <p class="text-xs font-semibold tracking-widest text-muted-foreground">TU ESPACIO EN HOMEATLAS</p>
          <h1 id="plugin-title" class="break-words font-heading text-3xl font-semibold text-heading">{{ manifest?.name ?? 'Módulo' }}</h1>
          <p class="max-w-xl text-sm leading-6 text-muted-foreground">Lo importante de tu hogar, organizado a tu manera.</p>
        </div>
      </div>
      <Badge v-if="manifest?.storage" variant="outline" class="gap-2 px-3 py-1.5"><AppIcon name="shield" />Acceso con permisos</Badge>
    </header>
    <p v-if="loading" role="status" class="text-muted-foreground">Cargando tu espacio…</p>
    <Alert v-else-if="!canRead"><AlertDescription>Este módulo no está disponible con tus permisos actuales.</AlertDescription></Alert>
    <Alert v-if="error" variant="destructive" role="alert"><AlertDescription>{{ error }}</AlertDescription><Button v-if="!manifest" variant="outline" @click="load">Reintentar</Button></Alert>
    <div v-if="manifest" class="grid min-w-0 items-start gap-6 lg:grid-cols-[256px_minmax(0,1fr)]">
      <aside v-if="manifest.views.length > 1" class="min-w-0">
        <div class="space-y-2 lg:hidden">
          <Label :for="pluginId + '-action'">Qué quieres hacer</Label>
          <Select :model-value="view?.id" @update:model-value="value => router.push('/' + pluginId + '/' + String(value))">
            <SelectTrigger :id="pluginId + '-action'" class="w-full"><SelectValue placeholder="Elige una acción" /></SelectTrigger>
            <SelectContent><SelectItem v-for="item in manifest.views" :key="item.id" :value="item.id">{{ item.title }}</SelectItem></SelectContent>
          </Select>
        </div>
        <nav aria-label="Vistas del módulo" class="hidden space-y-2 lg:block">
          <p class="px-4 pb-2 text-xs font-semibold tracking-widest text-muted-foreground">EXPLORAR</p>
          <ul class="space-y-1">
            <li v-for="item in manifest.views" :key="item.id">
              <Button as-child :variant="view?.id === item.id ? 'secondary' : 'ghost'" class="h-auto min-h-12 w-full justify-start gap-3 whitespace-normal px-4 py-3 text-left" :class="view?.id === item.id ? 'text-primary' : 'text-foreground'">
                <RouterLink :to="'/' + pluginId + '/' + item.id" :aria-current="view?.id === item.id ? 'page' : undefined">
                  <AppIcon :name="actionIcon(item)" class="size-5" />
                  <span class="min-w-0 flex-1"><span class="block text-sm font-semibold">{{ item.title }}</span><span class="mt-0.5 block text-xs font-normal text-muted-foreground">{{ actionLabel(item) }}</span></span>
                  <AppIcon v-if="view?.id === item.id" name="arrow" class="size-4" />
                </RouterLink>
              </Button>
            </li>
          </ul>
        </nav>
      </aside>
      <div v-if="view" class="min-w-0 space-y-6" :class="manifest.views.length === 1 ? 'lg:col-span-2' : ''">
        <Card>
          <CardHeader class="gap-3 border-b border-border pb-6">
            <div class="flex flex-wrap items-center justify-between gap-3"><CardTitle class="font-heading text-xl">{{ view.title }}</CardTitle><Badge variant="secondary">{{ actionLabel(view) }}</Badge></div>
            <CardDescription class="max-w-2xl leading-6">{{ view.description }}</CardDescription>
          </CardHeader>
          <CardContent>
            <form class="space-y-6" :aria-busy="busy" @submit.prevent="run">
              <div v-for="field in view.fields" :key="field.name" class="space-y-2">
                <Label :for="pluginId + '-' + field.name">{{ field.label }}</Label>
                <Textarea v-if="field.type === 'textarea'" :id="pluginId + '-' + field.name" v-model="input[field.name]" :required="field.required" :maxlength="2000" :disabled="busy || !canRun" class="min-h-48 leading-7" />
                <Input v-else :id="pluginId + '-' + field.name" v-model="input[field.name]" :type="field.type" :step="field.type === 'number' ? 'any' : undefined" :required="field.required" :maxlength="2000" :disabled="busy || !canRun" />
              </div>
              <div class="flex flex-wrap items-center justify-between gap-4 border-t border-border pt-5">
                <p class="text-sm text-muted-foreground">{{ canRun ? 'Revisa los datos antes de continuar.' : 'Necesitas permiso de ejecución para usar esta acción.' }}</p>
                <Button type="submit" :disabled="busy || !canRun" class="w-full gap-2 sm:w-auto"><AppIcon :name="actionIcon(view)" class="size-4" />{{ busy ? 'Procesando…' : view.submitLabel }}</Button>
              </div>
            </form>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><div class="flex items-center gap-3"><span class="flex size-10 items-center justify-center rounded-xl bg-secondary text-primary"><AppIcon name="document" class="size-5" /></span><div><CardTitle class="font-heading text-lg">Resultado</CardTitle><CardDescription>La respuesta de tu última acción.</CardDescription></div></div></CardHeader>
          <CardContent>
            <p v-if="result" role="status" class="whitespace-pre-wrap break-words rounded-xl border border-border bg-background p-5 text-base leading-7">{{ result }}</p>
            <div v-else class="rounded-xl border border-dashed border-border px-5 py-8 text-center"><p class="font-medium text-heading">Todo listo para empezar</p><p class="mt-2 text-sm leading-6 text-muted-foreground">Completa la acción de arriba y verás aquí su resultado.</p></div>
          </CardContent>
        </Card>
      </div>
      <p v-else-if="!loading">Esta vista no existe.</p>
    </div>
  </section>
</template>
