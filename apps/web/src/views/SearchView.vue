<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ApiError, get } from '@/core/api'
import { accountContext } from '@/core/account'
import { session } from '@/core/auth'
import { navigationModules } from '@/core/state'
import { moduleLink } from '@/core/home'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Card, CardContent } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import AppIcon from '@/components/AppIcon.vue'

interface Match { module: string; id: string; title: string; path: string }
const route = useRoute(), router = useRouter()
const query = ref(''), module = ref('__all'), matches = ref<Match[]>([]), searching = ref(false), searched = ref(false), error = ref('')
const context = computed(() => JSON.stringify([accountContext(), navigationModules.value.map(m => [m.id, m.version, m.navigation.path])]))
const visible = computed(() => matches.value.filter(match => navigationModules.value.some(m => m.id === match.module && moduleLink(match.path, m.navigation.path))))
let revision = 0
function clear() { ++revision; matches.value = []; searched.value = false; searching.value = false; error.value = '' }
watch([query, module], clear, { flush: 'sync' })
async function load() {
  clear()
  query.value = typeof route.query.q === 'string' ? route.query.q : ''
  module.value = typeof route.query.module === 'string' ? route.query.module : '__all'
  if (module.value !== '__all' && !navigationModules.value.some(m => m.id === module.value)) { error.value = 'Ese módulo no está disponible para tu cuenta. Elige otro filtro.'; return }
  const value = query.value.trim()
  if (!value || !session.value || !navigationModules.value.length) return
  if (new TextEncoder().encode(value).length > 200) { error.value = 'La búsqueda es muy larga. Escribe unas pocas palabras.'; return }
  const request = ++revision, scope = context.value
  searching.value = true
  const params = new URLSearchParams({ q: value })
  if (module.value !== '__all') params.set('module', module.value)
  try {
    const next = await get<Match[]>('/api/search?' + params.toString())
    if (request === revision && scope === context.value) { matches.value = next; searched.value = true }
  } catch (cause) { if (request === revision && scope === context.value) error.value = cause instanceof ApiError && cause.status === 401 ? 'Tu sesión ha terminado. Vuelve a iniciar sesión.' : 'No pudimos buscar. Comprueba la conexión e inténtalo de nuevo.' }
  finally { if (request === revision) searching.value = false }
}
watch([() => route.query.q, () => route.query.module, context], load, { immediate: true, flush: 'sync' })
watch(() => route.hash, async hash => { if (hash === '#global-query') { await nextTick(); document.getElementById('global-query')?.focus() } }, { immediate: true })
async function submit() {
  const value = query.value.trim(), selected = module.value === '__all' ? undefined : module.value
  if (new TextEncoder().encode(value).length > 200) { clear(); error.value = 'La búsqueda es muy larga. Escribe unas pocas palabras.'; return }
  if ((route.query.q ?? '') === value && route.query.module === selected) await load()
  else await router.push({ path: '/search', query: { q: value || undefined, module: selected }, hash: route.hash })
}
async function reset() { await router.push({ path: '/search', hash: '#global-query' }); if (!route.query.q) { query.value = ''; module.value = '__all'; clear() } }
onUnmounted(clear)
</script>
<template>
  <section aria-labelledby="global-search-title" class="space-y-8"><header><h1 id="global-search-title" class="text-3xl font-semibold tracking-tight">Buscar en mi hogar</h1><p class="mt-3 text-sm leading-6 text-muted-foreground">Encuentra por título, descripción o etiquetas el contenido al que tienes acceso.</p></header>
    <Card><CardContent class="p-6"><form role="search" class="space-y-5" :aria-busy="searching" @submit.prevent="submit"><div class="grid items-end gap-5 sm:grid-cols-[minmax(0,1fr)_minmax(12rem,16rem)]"><div class="space-y-2"><Label for="global-query">Qué quieres encontrar</Label><Input id="global-query" v-model="query" type="search" autocomplete="off" maxlength="200" placeholder="Un manual, una factura…" :disabled="!navigationModules.length" /></div><div class="space-y-2"><Label for="search-module">Dónde buscar</Label><Select v-model="module" :disabled="!navigationModules.length"><SelectTrigger id="search-module"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="__all">Todos mis módulos</SelectItem><SelectItem v-for="item in navigationModules" :key="item.id" :value="item.id">{{ item.navigation.label }}</SelectItem></SelectContent></Select></div></div><div class="flex flex-wrap gap-3"><Button type="submit" :disabled="!navigationModules.length"><AppIcon name="search" class="size-5" />Buscar</Button><Button type="button" variant="outline" @click="reset">Limpiar filtros</Button></div></form></CardContent></Card>
    <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
    <div v-if="!navigationModules.length" class="rounded-xl border bg-card p-6"><h2 class="font-semibold">Todavía no tienes módulos disponibles</h2><p class="mt-2 text-sm leading-6 text-muted-foreground">Los módulos activos para tu cuenta aparecerán aquí.</p><Button as-child variant="outline" class="mt-4"><RouterLink to="/modules">Ver mis módulos</RouterLink></Button></div>
    <section v-else aria-labelledby="search-results-title" aria-live="polite" :aria-busy="searching"><h2 id="search-results-title" class="text-xl font-semibold">{{ searching ? 'Buscando…' : 'Resultados' }}</h2>
      <p v-if="!searched && !searching && !error" class="mt-3 text-sm leading-6 text-muted-foreground">Escribe unas palabras y elige Buscar.</p><template v-if="searched"><p v-if="!visible.length" class="mt-3 text-sm leading-6 text-muted-foreground">No encontramos coincidencias. Prueba con otras palabras o cambia el módulo.</p><template v-else><p class="mt-3 text-sm text-muted-foreground">{{ visible.length }} {{ visible.length === 1 ? 'coincidencia' : 'coincidencias' }}. Se muestran hasta 50 por módulo; abre su biblioteca para explorar más.</p><ul class="mt-4 divide-y rounded-xl border bg-card px-5"><li v-for="match in visible" :key="match.module + ':' + match.id"><RouterLink :to="match.path" class="home-result"><AppIcon name="document" class="text-primary" /><span class="min-w-0 flex-1"><span class="block break-words font-medium">{{ match.title }}</span><span class="mt-1 block text-xs text-muted-foreground">{{ navigationModules.find(item => item.id === match.module)?.navigation.label }}</span></span><AppIcon name="arrow" class="size-5 text-muted-foreground" /></RouterLink></li></ul></template></template>
    </section>
  </section>
</template>
