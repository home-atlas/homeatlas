<script setup lang="ts">
import { Alert, AlertDescription } from '@/components/ui/alert'
import { ref } from 'vue'
import { ApiError, post } from '@/core/api'
import { refreshSession, session, smtpEnabled } from '@/core/auth'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
const busy = ref(false), notice = ref(''), error = ref('')
async function verify() {
  if (busy.value) return
  busy.value = true; notice.value = ''; error.value = ''
  try {
    await post('/api/auth/email/verification', {}, 30000)
    notice.value = 'Revisa tu buzón. El enlace de verificación caduca en una hora; si lo solicitas otra vez, se reemplaza el anterior.'
  } catch (cause) {
    error.value = cause instanceof ApiError && cause.status === 429 ? 'Hay demasiados intentos. Espera unos minutos.' : 'No pudimos enviar la verificación. Tu acceso local sigue funcionando; consulta con quien administra el servidor.'
  } finally { busy.value = false }
}
async function checkStatus() {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await refreshSession() }
  catch { error.value = 'No pudimos comprobar la verificación. Revisa tu conexión e inténtalo de nuevo.' }
  finally { busy.value = false }
}
</script>
<template>
  <Card v-if="session?.user.email"><CardHeader><CardTitle>Correo de tu cuenta</CardTitle></CardHeader><CardContent class="space-y-4"><p class="break-all text-sm font-medium">{{ session?.user.email }}</p><p class="text-sm leading-6 text-muted-foreground">{{ session?.user.emailVerified ? 'Correo verificado. Puedes recuperar acceso por correo cuando el envío esté habilitado.' : 'Tu correo identifica tu cuenta local. Verifica el buzón para habilitar la recuperación por correo.' }}</p><p v-if="!smtpEnabled" class="text-xs leading-6 text-muted-foreground">El envío de correos está desactivado en este servidor. Puedes seguir usando la recuperación local.</p><div v-else-if="!session?.user.emailVerified" class="flex flex-wrap gap-3"><Button :disabled="busy" @click="verify">{{ busy ? 'Procesando…' : 'Enviar verificación' }}</Button><Button variant="outline" :disabled="busy" @click="checkStatus">Actualizar estado</Button></div><Alert v-if="notice" role="status" class="text-sm leading-6 text-primary"><AlertDescription class="text-inherit leading-7">{{ notice }}</AlertDescription></Alert><Alert v-if="error" role="alert" class="text-sm leading-6 text-destructive" variant="destructive"><AlertDescription class="text-inherit leading-7">{{ error }}</AlertDescription></Alert></CardContent></Card>
</template>
