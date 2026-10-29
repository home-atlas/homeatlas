<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { Button } from '@/components/ui/button'
</script>

<template>
  <section class="space-y-5"><p class="text-sm text-muted-foreground">404</p><h1 class="text-3xl font-semibold">Esta página no está disponible</h1><p class="text-muted-foreground">El módulo puede estar desactivado o la dirección puede haber cambiado.</p><Button as-child><RouterLink to="/" replace>Volver a inicio</RouterLink></Button></section>
</template>
