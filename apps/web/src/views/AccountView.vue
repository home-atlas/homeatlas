<script setup lang="ts">
import { computed } from 'vue'
import { session } from '@/core/auth'
import { accountContext } from '@/core/account'
import AccountProfileCard from '@/components/AccountProfileCard.vue'
import AccountSessionsCard from '@/components/AccountSessionsCard.vue'
import AccountAccessCard from '@/components/AccountAccessCard.vue'
const context = computed(accountContext)
</script>
<template>
  <section aria-labelledby="account-title" class="space-y-8"><header><h1 id="account-title" class="text-3xl font-semibold tracking-tight">Mi cuenta</h1><p class="mt-3 text-sm leading-6 text-muted-foreground">Tu perfil, tus accesos y tus sesiones en {{ session?.household.name }}.</p></header>
    <template v-if="session"><AccountProfileCard :key="'profile:' + context" /><AccountSessionsCard :key="'sessions:' + context" /><AccountAccessCard :key="'access:' + context" /></template>
  </section>
</template>
