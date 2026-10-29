<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import BrandLogo from '@/components/BrandLogo.vue'
import AppIcon from '@/components/AppIcon.vue'
import AppBreadcrumbs from '@/components/AppBreadcrumbs.vue'
import AppNavigation from '@/components/AppNavigation.vue'
import { DialogRoot, DialogTrigger, DialogPortal, DialogOverlay, DialogContent, DialogTitle, DialogDescription, DialogClose, DropdownMenuRoot, DropdownMenuTrigger, DropdownMenuPortal, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator } from 'reka-ui'
import { connectionError } from '@/core/state'
import { logout, session } from '@/core/auth'
const reload = () => window.location.reload()
const route = useRoute()
const router = useRouter()
const isAuthPage = computed(() => ['/setup', '/login', '/join', '/activate-access', '/forgot-password', '/recover', '/verify-email'].includes(route.path))
const isSplitAuthPage = computed(() => ['/login', '/forgot-password', '/activate-access'].includes(route.path))
const mobileNavigation = ref(false)
const searchRequested = ref(false)
function restoreNavigationFocus(event: Event) {
  if (!searchRequested.value) return
  event.preventDefault(); searchRequested.value = false
  void nextTick(() => document.getElementById('home-query')?.focus())
}
watch(() => route.fullPath, () => { mobileNavigation.value = false })
const initials = computed(() => session.value?.user.name.trim().split(/\s+/).slice(0, 2).map(part => part[0]).join('').toUpperCase() || 'HA')
const signingOut = ref(false)
const logoutError = ref('')
async function signOut() {
  if (signingOut.value) return
  signingOut.value = true; logoutError.value = ''
  try {
    await logout()
    await router.replace('/login')
  } catch {
    logoutError.value = 'No pudimos confirmar el cierre de sesión. Comprueba la conexión y vuelve a intentarlo.'
  } finally { signingOut.value = false }
}
</script>

<template>
  <a href="#main-content" class="sr-only focus:not-sr-only focus:fixed focus:z-50 focus:bg-background focus:p-4">Saltar al contenido</a>
  <div class="min-h-screen" :class="session && !isAuthPage ? 'lg:grid lg:grid-cols-[var(--sidebar-width)_minmax(0,1fr)]' : ''">
    <aside v-if="session && !isAuthPage" class="app-sidebar sticky top-0 hidden h-dvh lg:block">
      <div class="app-sidebar-panel h-full overflow-y-auto"><AppNavigation /></div>
    </aside>
    <div class="min-w-0">
      <header v-if="!isSplitAuthPage" class="flex min-h-20 items-center justify-between gap-3 px-5 py-3 sm:px-8">
        <div class="flex min-w-0 items-center gap-3">
          <DialogRoot v-if="session && !isAuthPage" v-model:open="mobileNavigation">
            <DialogTrigger as-child><Button variant="ghost" size="icon" class="lg:hidden" aria-label="Abrir menú"><AppIcon name="menu" /></Button></DialogTrigger>
            <DialogPortal><DialogOverlay class="fixed inset-0 z-40 bg-heading/30" /><DialogContent @close-auto-focus="restoreNavigationFocus" class="fixed inset-y-0 left-0 z-50 w-72 max-w-[90vw] overflow-y-auto bg-card p-4 shadow-xl">
              <DialogTitle class="sr-only">Menú de tu hogar</DialogTitle><DialogDescription class="sr-only">Accede al inicio, los miembros y los módulos de tu hogar.</DialogDescription>
              <DialogClose as-child><Button variant="ghost" size="icon" class="absolute top-3 right-2" aria-label="Cerrar menú"><AppIcon name="close" /></Button></DialogClose>
              <AppNavigation @search="searchRequested = true" @navigate="mobileNavigation = false" />
            </DialogContent></DialogPortal>
          </DialogRoot>
          <template v-if="session"><AppIcon name="home" class="hidden size-5 text-muted-foreground sm:block" /><span class="truncate text-sm font-medium">{{ session.household.name }}</span></template>
          <RouterLink v-else to="/" aria-label="HomeAtlas, inicio"><BrandLogo /></RouterLink>
        </div>
        <DropdownMenuRoot v-if="session && route.path !== '/join'">
          <DropdownMenuTrigger as-child><Button variant="ghost" class="gap-2 px-1.5" :aria-label="'Mi cuenta: ' + session.user.name"><span class="flex size-10 items-center justify-center rounded-full border border-card bg-border/60 text-sm font-medium">{{ initials }}</span><AppIcon name="chevron" class="size-4 text-muted-foreground" /></Button></DropdownMenuTrigger>
          <DropdownMenuPortal><DropdownMenuContent align="end" :side-offset="8" class="z-50 min-w-60 max-w-[90vw] rounded-xl border bg-popover p-2 shadow-lg">
            <DropdownMenuLabel class="px-3 py-2"><span class="block break-words text-sm font-semibold text-heading">{{ session.user.name }}</span><span class="mt-1 block break-words text-xs font-normal text-muted-foreground">{{ session.household.name }}</span></DropdownMenuLabel>
            <DropdownMenuSeparator class="my-1 border-t" />
            <DropdownMenuItem as-child><RouterLink to="/settings" class="nav-item gap-3"><AppIcon name="settings" class="size-5" />Mi acceso y ajustes</RouterLink></DropdownMenuItem>
            <DropdownMenuItem :disabled="signingOut" class="nav-item cursor-pointer gap-3 data-disabled:opacity-50" @select="signOut"><AppIcon name="logout" class="size-5" />{{ signingOut ? 'Cerrando sesión…' : 'Cerrar sesión' }}</DropdownMenuItem>
          </DropdownMenuContent></DropdownMenuPortal>
        </DropdownMenuRoot>
      </header>
      <main id="main-content" tabindex="-1" class="mx-auto outline-none" :class="isSplitAuthPage ? 'w-full' : isAuthPage ? 'max-w-[var(--auth-max-width)] px-6 py-10 sm:px-10 sm:py-8' : route.path === '/' ? 'max-w-[var(--content-max-width)] px-5 pt-0 pb-8 sm:px-8' : 'max-w-[var(--content-max-width)] px-6 pt-4 pb-10 sm:px-10 sm:pb-12'">
        <p v-if="logoutError" role="alert" class="mb-6 rounded-lg bg-destructive/10 p-4 text-sm text-destructive">{{ logoutError }}</p>
        <div v-if="connectionError" role="alert" class="mb-8 rounded-xl border border-destructive/30 bg-card p-5"><h1 class="font-semibold">No pudimos conectar con HomeAtlas</h1><p class="mt-2 text-sm text-muted-foreground">{{ connectionError }}</p><Button variant="outline" class="mt-4" @click="reload">Reintentar conexión</Button></div>
        <template v-else><AppBreadcrumbs v-if="session && !isAuthPage && route.path !== '/'" /><RouterView /></template>
      </main>
    </div>
  </div>
</template>
