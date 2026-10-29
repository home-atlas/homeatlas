<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import AppIcon from '@/components/AppIcon.vue'
import { navigationModules } from '@/core/state'
import { session } from '@/core/auth'
import { ApiError, get } from '@/core/api'
import { canManageHousehold } from '@/core/household'
import { getWebModule, type ModuleStatus } from '@/core/modules'
import { moduleLink, type HomeContribution, type HomeItem } from '@/core/home'
import familyPhoto from '@/assets/home-family.jpg'
import spacesPhoto from '@/assets/home-spaces.jpg'

interface SearchResult { module: string; id: string; title: string; path: string }
const route = useRoute(), router = useRouter()
const now = ref(new Date())
const clock = setInterval(() => { now.value = new Date() }, 60000)
const hour = computed(() => {
  try { return Number(new Intl.DateTimeFormat('es', { hour: 'numeric', hourCycle: 'h23', timeZone: session.value?.household.timezone ?? 'UTC' }).format(now.value)) }
  catch { return now.value.getUTCHours() }
})
const greeting = computed(() => hour.value < 12 ? 'Buenos días' : hour.value < 20 ? 'Buenas tardes' : 'Buenas noches')
const canManage = computed(() => canManageHousehold(session.value))
interface HomeEntry { module: ModuleStatus; home: HomeContribution; items: HomeItem[]; loading: boolean; error: string; request: number }
const homeEntries = ref<HomeEntry[]>([])
const suggestions = computed(() => homeEntries.value.flatMap(entry => (entry.home.suggestions ?? []).map(suggestion => ({ ...suggestion, key: entry.module.id + ':' + suggestion.term }))))
function spaceHome(id: string) { return getWebModule(router, id)?.home }
const query = ref('')
const searched = ref(false)
const searching = ref(false)
const searchError = ref('')
const matches = ref<SearchResult[]>([])
const searchInput = ref<HTMLInputElement>()
let recentRevision = 0, searchRevision = 0
const context = computed(() => JSON.stringify([session.value?.user.id, session.value?.household.id, session.value?.household.timezone, session.value?.permissions, session.value?.role, navigationModules.value.map(m => [m.id, m.version, m.navigation.path])]))
const visibleMatches = computed(() => matches.value.filter(match => navigationModules.value.some(module => module.id === match.module && moduleLink(match.path, module.navigation.path))))
function message(error: unknown) {
  return error instanceof ApiError && error.status === 401 ? 'Tu sesión ha terminado. Vuelve a iniciar sesión.' : 'No pudimos cargar la información. Inténtalo de nuevo.'
}
function loadHomes() {
  const revision = ++recentRevision
  homeEntries.value = []
  if (!session.value) return
  homeEntries.value = navigationModules.value.flatMap(module => {
    const home = getWebModule(router, module.id)?.home
    return home ? [{ module, home, items: [], loading: false, error: '', request: 0 }] : []
  })
  for (const entry of homeEntries.value) void loadEntry(entry, revision)
}
async function loadEntry(entry: HomeEntry, revision = recentRevision) {
  const request = ++entry.request
  const current = () => revision === recentRevision && request === entry.request
  entry.items = []; entry.error = ''; entry.loading = true
  try {
    const items = await entry.home.load()
    if (current()) entry.items = items.filter(item => moduleLink(item.to, entry.module.navigation.path)).slice(0, 3)
  } catch (error) { if (current()) entry.error = message(error) }
  finally { if (current()) entry.loading = false }
}
function clearSearch() {
  ++searchRevision; matches.value = []; searched.value = false; searching.value = false; searchError.value = ''
}
watch(context, () => { clearSearch(); query.value = typeof route.query.q === 'string' ? route.query.q : ''; loadHomes(); if (query.value) void runSearch() }, { immediate: true, flush: 'sync' })
watch(query, clearSearch, { flush: 'sync' })
watch(() => route.hash, async hash => {
  if (hash === '#home-search') { await nextTick(); searchInput.value?.focus() }
}, { immediate: true, flush: 'post' })
onUnmounted(() => { clearInterval(clock); ++recentRevision; ++searchRevision })
async function closeSearch() {
  query.value = ''; clearSearch()
  await router.replace({ query: { ...route.query, q: undefined }, hash: route.hash })
  searchInput.value?.focus()
}
async function search(term = query.value) {
  const value = term.trim()
  if (new TextEncoder().encode(value).length > 200) { clearSearch(); searchError.value = 'La búsqueda es muy larga. Escribe unas pocas palabras.'; return }
  if ((route.query.q ?? '') === value) await runSearch(value)
  else await router.push({ query: { ...route.query, q: value || undefined }, hash: '#home-search' })
}
watch(() => route.query.q, () => { if (route.path === '/') { query.value = typeof route.query.q === 'string' ? route.query.q : ''; void runSearch() } })
async function runSearch(term = query.value) {
  query.value = term
  const value = term.trim()
  clearSearch()
  if (!value) { searchInput.value?.focus(); return }
  if (new TextEncoder().encode(value).length > 200) { searchError.value = 'La búsqueda es muy larga. Escribe unas pocas palabras.'; return }
  if (!session.value || !navigationModules.value.length) return
  const revision = ++searchRevision
  searching.value = true
  try { const result = await get<SearchResult[]>('/api/search?q=' + encodeURIComponent(value)); if (revision === searchRevision) { matches.value = result; searched.value = true } }
  catch (error) { if (revision === searchRevision) searchError.value = message(error) }
  finally { if (revision === searchRevision) searching.value = false }
}
</script>

<template>
  <div class="space-y-8 pb-2 sm:space-y-9">
    <section class="home-hero relative isolate overflow-hidden rounded-3xl" aria-labelledby="home-title">
      <div class="home-hero-photo absolute inset-0 -z-20" />
      <div class="home-hero-wash absolute inset-0 -z-10" />
      <div class="px-5 pt-7 pb-5 sm:px-8 sm:pt-8 sm:pb-6">
        <div class="mb-7 sm:mb-8">
          <div class="flex items-center gap-3"><h1 id="home-title" class="text-3xl leading-tight font-extrabold tracking-tight sm:text-[44px]">{{ greeting }}</h1><AppIcon :name="hour >= 20 || hour < 6 ? 'moon' : 'sun'" class="size-9 text-warning sm:size-10" /></div>
          <p class="mt-2 max-w-md text-base leading-7 text-muted-foreground sm:text-xl">Esto es lo importante de tu hogar.</p>
        </div>
        <section id="home-search" class="home-search-panel scroll-mt-6 rounded-2xl border border-card/80 bg-card/85 p-4 sm:p-6" aria-labelledby="search-title">
          <h2 id="search-title" class="text-xl font-extrabold tracking-tight sm:text-2xl">Encuentra en <span class="text-primary">HomeAtlas</span></h2>
          <p class="mt-1 text-sm leading-6 text-muted-foreground sm:text-base">La información de tu hogar, siempre a mano.</p>
          <form role="search" class="relative mt-5" :aria-busy="searching" @submit.prevent="search()">
            <label for="home-query" class="sr-only">¿Qué necesitas encontrar hoy?</label>
            <AppIcon name="search" class="pointer-events-none absolute top-1/2 left-4 z-10 size-6 -translate-y-1/2 text-muted-foreground" />
            <input id="home-query" ref="searchInput" v-model="query" type="search" autocomplete="off" maxlength="200" placeholder="¿Qué buscas?" class="form-input home-search-input pr-16 pl-13" :disabled="!navigationModules.length" aria-describedby="search-help" />
            <Button type="submit" size="icon" class="absolute top-1/2 right-2 -translate-y-1/2 rounded-xl" :disabled="!navigationModules.length" aria-label="Buscar en mi hogar"><AppIcon name="arrow" /></Button>
          </form>
          <p id="search-help" class="sr-only">Busca por título, descripción o etiquetas. Solo verás contenido al que tienes acceso.</p>
          <div v-if="suggestions.length" class="mt-4 grid gap-2 sm:grid-cols-3 sm:gap-3">
            <button v-for="suggestion in suggestions" :key="suggestion.key" type="button" class="home-suggestion" :class="'home-tint-' + suggestion.tone" @click="search(suggestion.term)"><AppIcon :name="suggestion.icon" class="size-6" /><span>{{ suggestion.label }}</span></button>
          </div>
          <p v-if="!navigationModules.length" class="mt-4 text-sm leading-6 text-muted-foreground">Todavía no tienes módulos disponibles para buscar. Puedes consultar tus accesos en <RouterLink to="/modules" class="font-semibold text-primary underline">Mis módulos</RouterLink>.</p>
        </section>
      </div>
    </section>

    <section v-if="searching || searched || searchError" class="rounded-2xl border bg-card p-5 sm:p-6" aria-labelledby="results-title" aria-live="polite" :aria-busy="searching">
      <div class="flex flex-wrap items-center justify-between gap-2"><h2 id="results-title" class="text-xl font-bold">{{ searching ? 'Buscando en tu hogar…' : 'Resultados de tu búsqueda' }}</h2><Button variant="ghost" @click="closeSearch">Cerrar búsqueda<AppIcon name="close" class="size-4" /></Button></div>
      <p v-if="searchError" role="alert" class="mt-3 text-sm text-destructive">{{ searchError }}</p>
      <template v-else-if="searched"><p v-if="!visibleMatches.length" class="mt-3 leading-7 text-muted-foreground">No encontramos resultados para «{{ query.trim() }}». Prueba con otra palabra del título, la descripción o las etiquetas.</p>
        <ul v-else class="mt-3 divide-y"><li v-for="match in visibleMatches" :key="match.module + ':' + match.id"><RouterLink :to="match.path" class="home-result"><span class="flex size-11 shrink-0 items-center justify-center rounded-xl bg-secondary text-primary"><AppIcon :name="spaceHome(match.module)?.icon ?? 'grid'" /></span><span class="min-w-0 flex-1"><span class="block break-words font-semibold text-heading">{{ match.title }}</span><span class="mt-1 block text-xs text-muted-foreground">{{ navigationModules.find(module => module.id === match.module)?.navigation.label }}</span></span><AppIcon name="arrow" class="size-5 text-muted-foreground" /></RouterLink></li></ul>
      </template>
    </section>

    <section aria-labelledby="for-you-title">
      <div class="mb-4 flex flex-wrap items-end justify-between gap-2"><div><p class="home-eyebrow">A mano, cuando lo necesitas</p><h2 id="for-you-title" class="home-section-title">Para ti</h2><p class="mt-1 text-sm text-muted-foreground">Pequeños pasos para un hogar más organizado.</p></div><RouterLink v-for="entry in homeEntries.filter(entry => moduleLink(entry.home.collection.to, entry.module.navigation.path))" :key="entry.module.id" :to="entry.home.collection.to" class="home-text-link">{{ entry.home.collection.label }}<AppIcon name="arrow" class="size-4" /></RouterLink></div>
      <div class="grid gap-3 md:grid-cols-2 min-[75rem]:grid-cols-3">
        <article v-for="entry in homeEntries" :key="entry.module.id" class="home-action-card" :class="'home-tint-' + entry.home.tone" :aria-busy="entry.loading">
          <img :src="entry.home.image" alt="" loading="lazy" width="64" height="96" class="home-action-image" />
          <div class="min-w-0 flex-1"><p class="home-card-caption"><AppIcon :name="entry.home.icon" class="size-4" />{{ entry.items[0] ? entry.home.recentCaption : entry.home.caption }}</p>
            <p v-if="entry.loading" class="mt-2 text-sm text-muted-foreground" role="status">{{ entry.home.loadingText }}</p>
            <template v-else-if="entry.error"><p role="alert" class="mt-2 text-sm text-destructive">{{ entry.error }}</p><Button variant="ghost" class="mt-1" @click="loadEntry(entry)">Reintentar</Button></template>
            <RouterLink v-else-if="entry.items[0] || moduleLink(entry.home.empty.to, entry.module.navigation.path)" :to="entry.items[0]?.to ?? entry.home.empty.to" class="home-card-link"><h3>{{ entry.items[0]?.title ?? entry.home.empty.title }}</h3><p>{{ entry.items[0]?.description ?? entry.home.empty.description }}</p><span class="home-arrow"><AppIcon name="arrow" class="size-4" /></span></RouterLink>
          </div>
        </article>
        <RouterLink to="/family" class="home-action-card home-tint-blue group"><img :src="familyPhoto" alt="" loading="lazy" width="64" height="96" class="home-action-image" /><div class="min-w-0 flex-1"><p class="home-card-caption"><AppIcon name="users" class="size-4" />Miembros del hogar</p><h3>{{ canManage ? 'Mejor, en familia' : 'Tu familia, cerca' }}</h3><p>{{ canManage ? 'Invita y ayuda a entrar a los tuyos.' : 'Consulta quién forma parte de tu hogar.' }}</p></div><span class="home-arrow"><AppIcon name="arrow" class="size-4" /></span></RouterLink>
        <RouterLink to="/settings" class="home-action-card home-tint-warm group"><img :src="spacesPhoto" alt="" loading="lazy" width="64" height="96" class="home-action-image" /><div class="min-w-0 flex-1"><p class="home-card-caption"><AppIcon name="shield" class="size-4" />Tu tranquilidad</p><h3>Tu acceso, a tu manera</h3><p>Revisa cómo entras a tu hogar.</p></div><span class="home-arrow"><AppIcon name="arrow" class="size-4" /></span></RouterLink>
      </div>
    </section>

    <section aria-labelledby="spaces-title">
      <div class="mb-4"><h2 id="spaces-title" class="home-section-title">Tus espacios</h2><p class="mt-1 text-sm text-muted-foreground">Explora y organiza la información de tu hogar.</p></div>
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <RouterLink v-for="module in navigationModules" :key="module.id" :to="module.navigation.path" class="home-space group home-space-module"><img :src="spaceHome(module.id)?.image ?? spacesPhoto" alt="" loading="lazy" width="1000" height="667" /><div class="home-space-wash" /><div class="relative z-10 max-w-[75%]"><span class="mb-3 flex size-11 items-center justify-center rounded-xl bg-secondary text-primary"><AppIcon :name="spaceHome(module.id)?.icon ?? 'grid'" /></span><h3>{{ module.navigation.label }}</h3><p>{{ spaceHome(module.id)?.space.description ?? 'Un espacio para lo que necesitas.' }}</p></div><span class="home-arrow"><AppIcon name="arrow" class="size-5" /></span></RouterLink>
        <RouterLink to="/family" class="home-space group home-space-family"><img :src="familyPhoto" alt="" loading="lazy" width="1000" height="667" /><div class="home-space-wash" /><div class="relative z-10 max-w-[75%]"><span class="mb-3 flex size-11 items-center justify-center rounded-xl bg-warning-muted text-warning-foreground"><AppIcon name="users" /></span><h3>Mi familia</h3><p>Un hogar que compartes con los tuyos.</p></div><span class="home-arrow"><AppIcon name="arrow" class="size-5" /></span></RouterLink>
        <RouterLink to="/modules" class="home-space group home-space-modules"><img :src="spacesPhoto" alt="" loading="lazy" width="1000" height="667" /><div class="home-space-wash" /><div class="relative z-10 max-w-[75%]"><span class="mb-3 flex size-11 items-center justify-center rounded-xl bg-secondary text-primary"><AppIcon name="home" /></span><h3>A tu medida</h3><p>Las herramientas que tu hogar necesita.</p></div><span class="home-arrow"><AppIcon name="arrow" class="size-5" /></span></RouterLink>
      </div>
    </section>

    <section v-for="entry in homeEntries.filter(entry => entry.items.length > 1)" :key="entry.module.id" :aria-labelledby="'recent-' + entry.module.id" class="rounded-2xl border border-border/70 bg-card p-5 sm:p-6">
      <h2 :id="'recent-' + entry.module.id" class="text-lg font-bold">{{ entry.home.recentTitle }}</h2><ul class="mt-3 divide-y"><li v-for="item in entry.items.slice(1)" :key="item.id"><RouterLink :to="item.to" class="home-result"><AppIcon :name="entry.home.icon" class="text-primary" /><span class="min-w-0 flex-1 break-words font-medium">{{ item.title }}</span><span v-if="item.date" class="hidden text-sm text-muted-foreground sm:block">{{ item.date }}</span><AppIcon name="arrow" class="size-4" /></RouterLink></li></ul>
    </section>
    <footer class="flex flex-wrap items-center justify-between gap-3 border-t border-border/70 pt-5 text-xs leading-5 text-muted-foreground"><p class="flex items-center gap-2"><AppIcon name="lock" class="size-4" />Tu hogar. Tus datos. Tu tranquilidad.</p><p>Solo tú y las personas con permiso pueden ver la información de tu hogar.</p></footer>
  </div>
</template>
