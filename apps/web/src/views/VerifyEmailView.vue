<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ApiError, post } from '@/core/api'
import { refreshSession } from '@/core/auth'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
const route = useRoute(), router = useRouter()
const code = ref(''), busy = ref(false), verified = ref(false), error = ref('')
watch(() => route.hash, async hash => {
  if (!hash) return
  code.value = hash.slice(1); error.value = ''; verified.value = false
  // Keep bearer tokens in memory and JSON bodies only. A GET never consumes verification.
  await router.replace('/verify-email')
}, { immediate: true })
async function confirm() {
  if (busy.value || !code.value) return
  busy.value = true; error.value = ''
  try {
    await post('/api/auth/email/verify', { code: code.value })
    code.value = ''; verified.value = true
    await refreshSession().catch(() => {})
  } catch (cause) {
    error.value = cause instanceof ApiError && cause.status === 410 ? 'El enlace caducó, se usó o fue reemplazado. Solicita otro desde Mi familia.' : 'No pudimos verificar el correo. Espera un momento e inténtalo de nuevo.'
  } finally { busy.value = false }
}
</script>
<template>
  <section aria-labelledby="verify-title" class="space-y-6"><h1 id="verify-title" class="text-3xl font-semibold tracking-tight">Verificar correo</h1><Card><CardHeader><CardTitle>{{ verified ? 'Correo verificado' : 'Confirma tu buzón' }}</CardTitle></CardHeader><CardContent class="space-y-5"><p v-if="verified" role="status" class="text-sm leading-6 text-muted-foreground">Tu cuenta ya puede solicitar recuperación por correo cuando SMTP esté habilitado. La verificación no cambia tus permisos ni inicia una sesión.</p><template v-else><p class="text-sm leading-6 text-muted-foreground">{{ code ? 'Pulsa el botón para confirmar que tienes acceso a este buzón.' : 'Abre el enlace completo del mensaje. Puedes solicitar uno nuevo desde Mi familia después de iniciar sesión.' }}</p><Button v-if="code" :disabled="busy" class="w-full" @click="confirm">{{ busy ? 'Verificando…' : 'Verificar correo' }}</Button></template><p v-if="error" role="alert" class="text-sm leading-6 text-destructive">{{ error }}</p><RouterLink to="/family" class="nav-item justify-center text-primary">Ir a Mi familia</RouterLink></CardContent></Card></section>
</template>
