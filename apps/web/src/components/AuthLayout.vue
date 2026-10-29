<script setup lang="ts">
import { Card } from '@/components/ui/card'
import BrandLogo from '@/components/BrandLogo.vue'
import homeImage from '@/assets/login-home.jpg'
</script>

<template>
  <div class="auth-layout">
    <section aria-labelledby="auth-story-title" class="auth-story relative isolate hidden overflow-hidden lg:block">
      <img :src="homeImage" alt="" width="1086" height="1448" fetchpriority="high" class="absolute inset-0 -z-20 size-full object-cover object-bottom" />
      <div class="auth-story-content relative flex h-full flex-col p-10 xl:p-12">
        <div class="relative isolate">
          <div class="auth-story-shade absolute -inset-x-12 -top-12 -bottom-24 -z-10" />
          <BrandLogo size="large" />
          <div class="mt-12 xl:mt-14">
            <h2 id="auth-story-title" class="auth-headline font-extrabold tracking-tight">Todo tu hogar,<br /><span class="relative inline-block text-primary">en un solo lugar.<svg viewBox="0 0 180 12" fill="none" aria-hidden="true" class="absolute -bottom-3 left-1/2 h-3 w-36 -translate-x-1/2 text-warning"><path d="M4 9C52 2 119 1 176 7" stroke="currentColor" stroke-width="3" stroke-linecap="round" /></svg></span></h2>
            <p class="mt-8 max-w-sm text-base leading-7 text-muted-foreground">Tus documentos, tu familia y lo importante de casa. Un espacio más simple, más organizado y siempre tuyo.</p>
          </div>
          <ul class="mt-8 space-y-5">
            <li class="flex items-center gap-4"><span class="grid size-12 shrink-0 place-items-center rounded-2xl bg-secondary text-primary"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="size-6"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" /><path d="M14 2v6h6M8 13h8M8 17h6" /></svg></span><div><h3 class="text-base font-bold">Documentos a mano</h3><p class="mt-1 text-sm text-muted-foreground">Contratos, manuales y archivos importantes.</p></div></li>
            <li class="flex items-center gap-4"><span class="grid size-12 shrink-0 place-items-center rounded-2xl bg-warning-muted text-warning-foreground"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="size-6"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M22 21v-2a4 4 0 0 0-3-3.87" /><circle cx="9" cy="7" r="4" /><path d="M16 3.13a4 4 0 0 1 0 7.75" /></svg></span><div><h3 class="text-base font-bold">Un hogar compartido</h3><p class="mt-1 text-sm text-muted-foreground">Con las personas que tú elijas.</p></div></li>
            <li class="flex items-center gap-4"><span class="grid size-12 shrink-0 place-items-center rounded-2xl bg-card/85 text-heading"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="size-6"><path d="M12 22s8-4 8-11V5l-8-3-8 3v6c0 7 8 11 8 11Z" /><path d="m9 12 2 2 4-4" /></svg></span><div><h3 class="text-base font-bold">Tus datos, en casa</h3><p class="mt-1 text-sm text-muted-foreground">Privados y guardados en tu servidor.</p></div></li>
          </ul>
        </div>
      </div>
    </section>

    <Card class="auth-form-panel w-full justify-center px-6 py-8 sm:px-10 sm:py-10">
      <slot />
    </Card>
  </div>
</template>

<style scoped>
.auth-layout { display: grid; align-items: center; min-height: 100svh; padding: 1rem; }
.auth-form-panel { max-width: var(--auth-max-width); margin-inline: auto; box-shadow: 0 12px 48px -32px color-mix(in srgb, var(--heading) 18%, transparent); }
.auth-story { --auth-scene-space: 13rem; min-height: 100svh; align-self: stretch; }
.auth-story-content { padding-bottom: var(--auth-scene-space); }
.auth-headline { font-size: clamp(2.75rem, 3.8vw, 3.75rem); line-height: 1.12; }
.auth-story-shade { background: linear-gradient(180deg, color-mix(in srgb, var(--background) 96%, transparent) 0%, color-mix(in srgb, var(--background) 90%, transparent) calc(100% - 6rem), transparent 100%); }
@media (min-width: 640px) { .auth-layout { padding: 1.5rem; } }
@media (min-width: 1024px) {
  .auth-layout { grid-template-columns: repeat(2, minmax(0, 1fr)); padding: 0; }
  .auth-form-panel { max-width: none; align-self: stretch; border: 0; border-radius: 0; box-shadow: none; }
}
@media (min-width: 1024px) and (max-height: 850px) {
  .auth-story { --auth-scene-space: 6rem; }
  .auth-form-panel { padding-block: 2rem; }
}
</style>
