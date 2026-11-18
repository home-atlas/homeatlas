<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { get, patch } from '@/core/api'
import { refreshSession } from '@/core/auth'
import { accountContext, accountError, type AccountProfile } from '@/core/account'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'

const profile = ref<AccountProfile>(), name = ref(''), loading = ref(false), busy = ref(false), error = ref(''), notice = ref('')
let revision = 0
async function load() {
  const request = ++revision, context = accountContext()
  loading.value = true; profile.value = undefined; error.value = ''
  try {
    const next = await get<AccountProfile>('/api/auth/profile')
    if (request !== revision || context !== accountContext()) return
    profile.value = next; name.value = next.user.name
  } catch (cause) { if (request === revision && context === accountContext()) error.value = accountError(cause) }
  finally { if (request === revision) loading.value = false }
}
async function save() {
  if (busy.value) return
  const request = ++revision, context = accountContext()
  const current = () => request === revision && context === accountContext()
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const next = await patch<AccountProfile>('/api/auth/profile', { name: name.value.trim() })
    if (!current()) return
    profile.value = next; name.value = next.user.name
    await refreshSession()
    if (current()) notice.value = 'Tu nombre se ha guardado.'
  } catch (cause) { if (current()) error.value = accountError(cause) }
  finally { if (current()) busy.value = false }
}
onMounted(load)
onUnmounted(() => { ++revision })
</script>
<template>
  <Card><CardHeader><CardTitle><h2>Mi perfil</h2></CardTitle><CardDescription>El nombre con el que apareces en tu hogar.</CardDescription></CardHeader><CardContent class="space-y-6">
    <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
    <p v-if="notice" role="status" class="text-sm text-primary">{{ notice }}</p>
    <p v-if="loading" role="status" class="text-sm text-muted-foreground">Cargando tu perfil…</p>
    <Button v-else-if="!profile" variant="outline" @click="load">Reintentar perfil</Button>
    <template v-if="profile">
      <form class="max-w-xl space-y-5" :aria-busy="busy" @submit.prevent="save"><fieldset :disabled="busy" class="min-w-0 space-y-5">
        <div class="space-y-2"><Label for="profile-name">Tu nombre</Label><Input id="profile-name" v-model="name" name="name" autocomplete="name" maxlength="80" required /></div>
        <Button type="submit" :disabled="busy">{{ busy ? 'Guardando…' : 'Guardar mi nombre' }}</Button>
      </fieldset></form>
      <dl class="grid gap-5 text-sm sm:grid-cols-2"><div class="min-w-0"><dt class="text-muted-foreground">Usuario de acceso</dt><dd class="mt-1 break-words font-medium">{{ profile.user.username }}</dd></div><div class="min-w-0"><dt class="text-muted-foreground">Correo</dt><dd class="mt-1 flex flex-wrap items-center gap-2 break-all"><span>{{ profile.user.email || 'Sin correo configurado' }}</span><Badge v-if="profile.user.emailVerified" variant="secondary">Verificado</Badge></dd></div></dl>
    </template>
  </CardContent></Card>
</template>
