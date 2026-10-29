<script setup lang="ts">
import { Alert, AlertDescription } from '@/components/ui/alert'
import { computed, reactive, ref } from 'vue'
import { post } from '@/core/api'
import { session } from '@/core/auth'
import { accessError } from '@/core/alternative-access'
import type { Family } from '@/core/household'
import { Input, PasswordInput } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import AppIcon from '@/components/AppIcon.vue'
import ActionDialog from '@/components/ActionDialog.vue'
import GrantFields from '@/components/GrantFields.vue'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
const props = defineProps<{ family: Family }>()
const emit = defineEmits<{ created: [] }>()
const open = ref(false), busy = ref(false), mode = ref<'create' | 'renew'>('create'), target = ref(''), error = ref(''), url = ref(''), notice = ref('')
const form = reactive({ name: '', username: '', email: '', role: 'restricted', permissions: ['documents.read'], password: '' })
const roles = computed(() => props.family.roles.filter(role => role !== 'admin'))
const capabilities = computed(() => props.family.capabilities.filter(cap => !cap.startsWith('household.')))
const assisted = computed(() => props.family.members.filter(member => member.assisted && member.id !== session.value?.user.id && !['owner', 'admin'].includes(member.role)))
function begin() { mode.value = 'create'; target.value = ''; Object.assign(form, { name: '', username: '', email: '', role: 'restricted' }); open.value = true; error.value = ''; url.value = ''; notice.value = ''; form.password = ''; form.permissions = capabilities.value.includes('documents.read') ? ['documents.read'] : [] }
function close() { if (busy.value) return; form.password = ''; url.value = ''; error.value = ''; notice.value = ''; open.value = false }
async function save() {
  if (busy.value) return
  busy.value = true; error.value = ''; url.value = ''; notice.value = ''
  try {
    const result = mode.value === 'create'
      ? await post<{ url: string }>('/api/household/assisted-members', form)
      : await post<{ url: string }>('/api/household/members/' + encodeURIComponent(target.value) + '/enrollment', { password: form.password })
    url.value = result.url; notice.value = 'El enlace está listo. Ahora sigue estos pasos con la persona a la que ayudas.'
    emit('created')
  } catch (cause) { error.value = accessError(cause) }
  finally { form.password = ''; busy.value = false }
}
async function copy() {
  try { await navigator.clipboard.writeText(url.value); notice.value = 'Enlace copiado. Compártelo solo con la persona que estás ayudando.' }
  catch { notice.value = 'Selecciona el enlace y cópialo manualmente.' }
}
</script>
<template>
  <Button variant="outline" @click="begin"><AppIcon name="users" class="size-5" />Preparar acceso con ayuda</Button>
  <ActionDialog v-if="open" title="Preparar acceso con ayuda" description="Primero prepara el enlace. Después, la persona elegirá su PIN en su propio equipo." :busy="busy" @close="close">
    <template v-if="url"><div class="space-y-5"><Alert role="status" class="rounded-xl bg-secondary p-4 text-base leading-7 text-muted-foreground"><AlertDescription class="text-inherit leading-7">{{ notice }}</AlertDescription></Alert><ol class="list-decimal space-y-3 pl-6 text-base leading-7"><li>Copia el enlace y compártelo solo con la persona a la que ayudas.</li><li>Pídele que lo abra en su teléfono o computadora personal, en el navegador que usará para entrar.</li><li>Allí elegirá y repetirá su PIN. Después podrá entrar sin correo ni contraseña.</li></ol><p class="rounded-xl border p-4 text-base leading-7"><strong>Úsalo en los próximos 15 minutos.</strong> Funciona una sola vez. Si caduca, prepara otro para la misma cuenta.</p><Label for="assisted-link" class="form-label">Enlace privado para preparar el PIN</Label><Input id="assisted-link" :model-value="url" readonly class="form-input" @focus="($event.target as HTMLInputElement).select()" /><p class="text-base leading-7 text-muted-foreground">No lo abras en tu equipo: el PIN quedaría preparado aquí. Cuando la persona termine, se cerrarán sus sesiones anteriores y dejarán de funcionar sus otros equipos con PIN. Su cuenta y documentos se conservarán.</p><div class="flex flex-wrap gap-3"><Button size="lg" @click="copy">Copiar enlace privado</Button><Button variant="outline" size="lg" @click="close">Terminar</Button></div></div></template>
    <form v-else class="space-y-6" :aria-busy="busy" @submit.prevent="save"><fieldset :disabled="busy" class="space-y-5"><div class="space-y-2"><Label for="assisted-mode" class="form-label">Qué quieres hacer</Label><Select :model-value="mode" @update:model-value="value => mode = value === 'renew' ? 'renew' : 'create'"><SelectTrigger id="assisted-mode"><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="create">Preparar una cuenta nueva</SelectItem><SelectItem v-if="assisted.length" value="renew">Ayudar a alguien que ya tiene cuenta</SelectItem></SelectGroup></SelectContent></Select></div>
      <template v-if="mode === 'create'"><div class="space-y-2"><Label for="assisted-name" class="form-label">Nombre de la persona</Label><Input id="assisted-name" v-model="form.name" maxlength="80" required autocomplete="off" class="form-input" /></div><div class="space-y-2"><Label for="assisted-username" class="form-label">Nombre de usuario</Label><Input id="assisted-username" v-model="form.username" pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,39}" minlength="3" maxlength="40" required autocapitalize="none" :spellcheck="false" autocomplete="off" class="form-input" /><p class="text-base leading-7 text-muted-foreground">Por ejemplo, maria. Usa de 3 a 40 letras sin acentos, números, puntos o guiones, sin espacios. La persona no tendrá que recordarlo para entrar con PIN.</p></div><div class="space-y-2"><Label for="assisted-email" class="form-label">Correo electrónico (opcional)</Label><Input id="assisted-email" v-model="form.email" type="email" maxlength="254" autocomplete="off" class="form-input" /></div><div class="space-y-3 rounded-xl border p-4"><h3 class="text-base font-semibold">Qué podrá hacer en el hogar</h3><p class="text-base leading-7 text-muted-foreground">Por defecto, solo podrá ver sus documentos y los que le compartan. Revisa estos permisos antes de crear el enlace.</p><GrantFields v-model:role="form.role" v-model:permissions="form.permissions" :roles="roles" :capabilities="capabilities" prefix="assisted" /></div></template>
      <div v-else class="space-y-2"><Label for="assisted-target" class="form-label">Miembro al que ayudas</Label><Select v-model="target" required><SelectTrigger id="assisted-target"><SelectValue placeholder="Selecciona una persona" /></SelectTrigger><SelectContent><SelectGroup><SelectItem v-for="member in assisted" :key="member.id" :value="member.id">{{ member.name }}</SelectItem></SelectGroup></SelectContent></Select><p class="text-base leading-7 text-muted-foreground">Elige su cuenta actual; no crees otra. Podrá elegir un nuevo PIN sin perder sus documentos. Cuando termine, se cerrarán sus sesiones anteriores y dejarán de funcionar sus otros equipos con PIN.</p></div>
      <div class="space-y-2"><Label for="assisted-proof" class="form-label">Tu contraseña de administrador</Label><PasswordInput id="assisted-proof" v-model="form.password" autocomplete="current-password" maxlength="256" required class="form-input" :disabled="busy" visibility-label="contraseña de administrador" /></div><p class="text-base leading-7 text-muted-foreground">Esta es tu contraseña, no la de la persona a la que ayudas. Ella elegirá su PIN al abrir el enlace.</p><Alert v-if="error" role="alert" class="rounded-lg bg-danger-muted p-4 text-base leading-7 text-destructive" variant="destructive"><AlertDescription class="text-inherit leading-7">{{ error }}</AlertDescription></Alert><div class="flex flex-wrap gap-3"><Button type="button" variant="outline" @click="close">Cancelar</Button><Button type="submit">{{ busy ? 'Preparando…' : 'Crear enlace privado' }}</Button></div>
    </fieldset></form>
  </ActionDialog>
</template>
