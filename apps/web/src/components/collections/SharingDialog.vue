<script setup lang="ts">
import { useCollection } from './model'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import ActionDialog from '@/components/ActionDialog.vue'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ApiError, get, put, remove } from '@/core/api'
import { session, setAuthState } from '@/core/auth'
import { householdError, type Family } from '@/core/household'
const { collectionId } = useCollection()

const props = defineProps<{ id: string; returnFocus?: () => HTMLElement | null }>()
const emit = defineEmits<{ close: []; changed: [count: number] }>()
const router = useRouter()
const grants = ref<{ userId: string; name: string; access: string }[]>([]), family = ref<Family>(), loading = ref(true), busy = ref(false), error = ref(''), target = ref('')
const canShare = computed(() => session.value?.permissions.includes(collectionId + '.share'))
const candidates = computed(() => family.value?.members.filter(m => m.id !== session.value?.user.id && m.permissions.includes(collectionId + '.read') && !grants.value.some(g => g.userId === m.id)) ?? [])
async function failed(err: unknown) {
  error.value = householdError(err)
  if (err instanceof ApiError && err.status === 401) { setAuthState({ setupRequired: false, session: null }); await router.replace('/login?redirect=' + encodeURIComponent('/' + collectionId + '/' + props.id)) }
}
async function load() {
  loading.value = true
  try {
    const [members, recipients] = await Promise.all([get<Family>('/api/household/members'), get<typeof grants.value>('/api/' + collectionId + '/' + encodeURIComponent(props.id) + '/shares')])
    family.value = members; grants.value = recipients; emit('changed', recipients.length)
  } catch (err) { await failed(err) } finally { loading.value = false }
}
async function change(user: string, revoke = false) {
  if (!user || busy.value) return; busy.value = true; error.value = ''
  try {
    const path = '/api/' + collectionId + '/' + encodeURIComponent(props.id) + '/shares/' + encodeURIComponent(user)
    if (revoke) await remove(path); else await put(path)
    target.value = ''; await load()
  } catch (err) { await failed(err) } finally { busy.value = false }
}
onMounted(load)
</script>
<template>
  <ActionDialog :return-focus="returnFocus" title="Acceso al archivo" description="Comparte solo con las personas que necesitan verlo. No se crean enlaces públicos." :busy="busy" @close="emit('close')"><div class="space-y-6"><Alert role="note"><AlertDescription class="leading-6">Compartir permite consultar y descargar. No permite editar ni volver a compartir el archivo.</AlertDescription></Alert><Alert v-if="error" role="alert" variant="destructive"><AlertDescription class="leading-6">{{ error }}</AlertDescription></Alert><p v-if="loading" aria-live="polite" class="text-sm text-muted-foreground">Comprobando accesos…</p><template v-else-if="family"><section aria-labelledby="recipients-title"><h3 id="recipients-title" class="font-medium">Quién tiene acceso</h3><p class="mt-3 text-sm">Tú, como propietario del archivo.</p><ul class="mt-3 divide-y"><li v-for="grant in grants" :key="grant.userId" class="flex flex-wrap items-center justify-between gap-3 py-4"><div class="min-w-0"><p class="break-words text-sm font-medium">{{ grant.name }}</p><Badge variant="secondary" class="mt-2">Lectura y descarga</Badge></div><Button v-if="canShare" variant="outline" size="sm" :disabled="busy" :aria-label="'Retirar acceso a ' + grant.name" @click="change(grant.userId, true)">Retirar acceso</Button></li></ul><p v-if="!grants.length" class="mt-3 text-xs text-muted-foreground">Ningún otro miembro tiene acceso.</p></section><form v-if="canShare" class="space-y-4 border-t pt-5" @submit.prevent="change(target)"><div class="space-y-2"><Label for="share-member">Compartir con un miembro</Label><Select v-model="target" :disabled="busy || !candidates.length" required><SelectTrigger id="share-member"><SelectValue placeholder="Selecciona un miembro" /></SelectTrigger><SelectContent><SelectGroup><SelectItem v-for="member in candidates" :key="member.id" :value="member.id">{{ member.name }}</SelectItem></SelectGroup></SelectContent></Select></div><p v-if="!candidates.length" class="text-xs leading-6 text-muted-foreground">No hay más miembros con permiso de lectura. Puedes invitarlos o revisar sus permisos en Mi familia.</p><Button type="submit" :disabled="busy || !target">{{ busy ? 'Guardando…' : 'Dar acceso de lectura' }}</Button></form><p v-else class="text-sm leading-6 text-muted-foreground">Necesitas permiso para compartir si quieres cambiar estos accesos.</p></template><Button v-else variant="outline" @click="load">Reintentar</Button></div></ActionDialog>
</template>
