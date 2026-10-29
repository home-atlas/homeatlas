<script setup lang="ts">
import { Alert, AlertDescription } from '@/components/ui/alert'
import { computed, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from '@/components/ui/dropdown-menu'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import AppIcon from '@/components/AppIcon.vue'
import ActionDialog from '@/components/ActionDialog.vue'
import GrantFields from '@/components/GrantFields.vue'
import AssistedAccessCard from '@/components/AssistedAccessCard.vue'
import EmailVerificationCard from '@/components/EmailVerificationCard.vue'
import { formatHouseholdDate } from '@/core/datetime'
import { ApiError, get, patch, post, remove } from '@/core/api'
import { refreshSession, session, setAuthState, smtpEnabled } from '@/core/auth'
import { householdError, invitationNotice, roleLabel, permissionLabel, statusLabel, type Family, type Invitation, type Member } from '@/core/household'

const router = useRouter(), route = useRoute()
const family = ref<Family>(), invitations = ref<Invitation[]>([]), loading = ref(true), busy = ref(false), error = ref(''), notice = ref('')
const invitationsError = ref(''), search = ref('')
const tab = computed({ get: () => family.value?.canManage && route.query.tab === 'invitations' ? 'invitations' : 'members', set: value => { void router.push({ query: { ...route.query, tab: value === 'invitations' ? 'invitations' : undefined } }) } })
const inviteOpen = ref(false), emailOpen = ref(false), inviteError = ref(''), inviteNotice = ref('')
const email = ref(''), role = ref('member'), permissions = ref<string[]>(['documents.read']), inviteURL = ref(''), sendEmail = ref(false)
const selected = ref<Member>(), memberAction = ref<'view' | 'edit' | 'remove'>('view'), editRole = ref('member'), editPermissions = ref<string[]>([])
const revoking = ref<Invitation>()
const memberFocusId = ref('')
const returnMemberFocus = () => document.getElementById('member-actions-' + memberFocusId.value) ?? document.getElementById('family-search')
const context = computed(() => JSON.stringify([session.value?.user.id, session.value?.household.id, session.value?.role, session.value?.permissions]))
let revision = 0, mounted = true
const fold = (value: string) => value.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLocaleLowerCase('es')
const members = computed(() => family.value?.members.filter(member => fold([member.name, member.username, member.email].filter(Boolean).join(' ')).includes(fold(search.value.trim()))) ?? [])
const initials = (name: string) => name.trim().split(/\s+/).slice(0, 2).map(word => word[0]).join('').toUpperCase()
const current = (account: string) => mounted && context.value === account
async function failed(err: unknown) {
  if (err instanceof ApiError && err.status === 401) { setAuthState({ setupRequired: false, session: null }); await router.replace('/login?redirect=' + encodeURIComponent(route.fullPath)) }
}
async function load() {
  const request = ++revision, account = context.value
  if (!session.value) { loading.value = false; return }
  loading.value = true; error.value = ''; invitationsError.value = ''
  try {
    const result = await get<Family>('/api/household/members')
    if (request !== revision || !current(account)) return
    family.value = result
    if (!result.capabilities.includes('documents.read')) permissions.value = []
    if (result.canManage) {
      try {
        const list = await get<Invitation[]>('/api/household/invitations')
        if (request === revision && current(account)) invitations.value = list
      } catch (cause) {
        if (request !== revision || !current(account)) return
        invitationsError.value = householdError(cause); await failed(cause)
      }
    } else { invitations.value = []; closeInvite() }
  } catch (cause) {
    if (request !== revision || !current(account)) return
    error.value = householdError(cause)
    if (cause instanceof ApiError && [401, 403].includes(cause.status)) family.value = undefined
    await failed(cause)
  } finally { if (request === revision && current(account)) loading.value = false }
}
watch(context, () => {
  ++revision; family.value = undefined; invitations.value = []; selected.value = undefined; revoking.value = undefined
  inviteOpen.value = false; inviteURL.value = ''; inviteError.value = ''; inviteNotice.value = ''; emailOpen.value = false
  notice.value = ''; error.value = ''; search.value = ''; busy.value = false
  void load()
}, { immediate: true, flush: 'sync' })
onUnmounted(() => { mounted = false; ++revision })
function beginInvite() {
  error.value = ''; email.value = ''; role.value = 'member'; permissions.value = family.value?.capabilities.includes('documents.read') ? ['documents.read'] : []
  sendEmail.value = smtpEnabled.value; inviteURL.value = ''; inviteError.value = ''; inviteNotice.value = ''; inviteOpen.value = true
}
function closeInvite() { if (busy.value) return; inviteOpen.value = false; inviteURL.value = ''; email.value = ''; inviteError.value = ''; inviteNotice.value = '' }
async function invite() {
  if (busy.value || !family.value?.canManage) return
  const account = context.value
  busy.value = true; inviteError.value = ''; inviteNotice.value = ''; inviteURL.value = ''
  try {
    const link = await post<{ url: string; emailStatus: string }>('/api/household/invitations', { email: email.value, role: role.value, permissions: permissions.value, sendEmail: smtpEnabled.value && sendEmail.value }, 30000)
    if (!current(account)) return
    inviteURL.value = link.url; email.value = ''; inviteNotice.value = invitationNotice(link.emailStatus)
    await load()
  } catch (cause) { if (current(account)) { inviteError.value = householdError(cause); await failed(cause) } }
  finally { if (current(account)) busy.value = false }
}
async function copy() {
  const account = context.value
  try { await navigator.clipboard.writeText(inviteURL.value); if (current(account) && inviteOpen.value) inviteNotice.value = 'Enlace copiado. Compártelo solo con la persona invitada.' }
  catch { if (current(account) && inviteOpen.value) inviteNotice.value = 'Selecciona el enlace y cópialo manualmente.' }
}
function canEdit(member: Member) { return family.value?.canManage && member.id !== session.value?.user.id && member.role !== 'owner' && (session.value?.role === 'owner' || member.role !== 'admin') }
function edit(member: Member, action: 'view' | 'edit' | 'remove' = 'view') {
  memberFocusId.value = member.id
  error.value = ''; selected.value = member; memberAction.value = action; editRole.value = member.role; editPermissions.value = member.permissions.filter(p => p !== 'household.manage')
}
async function saveMember() {
  if (!selected.value || !canEdit(selected.value) || memberAction.value === 'view' || busy.value) return
  const account = context.value, removing = memberAction.value === 'remove'
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const path = '/api/household/members/' + encodeURIComponent(selected.value.id)
    if (removing) await remove(path)
    else await patch(path, { role: editRole.value, permissions: editPermissions.value })
    if (!current(account)) return
    selected.value = undefined; notice.value = removing ? 'Miembro retirado. Sus sesiones y accesos a este hogar quedan revocados.' : 'Rol y permisos actualizados.'
    await refreshSession()
    if (current(account)) await load()
  } catch (cause) { if (current(account)) { error.value = householdError(cause); await failed(cause) } }
  finally { if (current(account)) busy.value = false }
}
async function revoke() {
  if (busy.value || !revoking.value || !family.value?.canManage) return
  const account = context.value
  busy.value = true; error.value = ''
  try {
    await remove('/api/household/invitations/' + encodeURIComponent(revoking.value.id))
    if (!current(account)) return
    revoking.value = undefined; notice.value = 'Invitación revocada. El enlace ya no puede usarse.'; await load()
  } catch (cause) { if (current(account)) { error.value = householdError(cause); await failed(cause) } }
  finally { if (current(account)) busy.value = false }
}
</script>

<template>
  <section aria-labelledby="family-title" class="space-y-7">
    <header class="flex flex-wrap items-start justify-between gap-5">
      <div class="max-w-md"><p class="mb-3 text-xs font-semibold tracking-widest text-primary uppercase">Las personas de tu hogar</p><div class="flex flex-wrap items-center gap-3"><h1 id="family-title" class="text-3xl font-bold tracking-tight">Familia</h1><Badge v-if="family" variant="secondary">{{ family.members.length }} {{ family.members.length === 1 ? 'miembro' : 'miembros' }}</Badge></div><p class="mt-3 text-sm leading-7 text-muted-foreground">Un lugar para compartir lo importante con los tuyos. Tú decides quién puede ver cada documento.</p></div>
      <div v-if="family?.canManage" class="flex w-full flex-wrap gap-3 sm:w-auto"><AssistedAccessCard :family="family" @created="load" /><Button :disabled="busy" @click="beginInvite"><AppIcon name="plus" class="size-5" />Invitar a alguien</Button></div>
    </header>
    <Alert v-if="error && !selected && !revoking" role="alert" class="rounded-xl border border-destructive/30 bg-danger-muted p-4 text-sm text-destructive" variant="destructive"><AlertDescription class="text-inherit leading-7">{{ error }}</AlertDescription></Alert>
    <Alert v-if="notice" role="status" class="flex gap-3 rounded-xl border border-primary/15 bg-secondary p-4 text-sm leading-6 text-primary"><AlertDescription class="text-inherit leading-7"><AppIcon name="shield" class="size-5" />{{ notice }}</AlertDescription></Alert>
    <Alert v-if="loading && !family" role="status" class="rounded-2xl border bg-card p-8 text-muted-foreground"><AlertDescription class="text-inherit leading-7">Cargando a las personas de tu hogar…</AlertDescription></Alert>
    <Button v-if="!loading && !family" variant="outline" @click="load">Reintentar</Button>
    <template v-if="family">
      <Tabs v-model="tab" default-value="members">
        <TabsList aria-label="Personas e invitaciones"><TabsTrigger value="members"><AppIcon name="users" class="size-4" />Miembros</TabsTrigger><TabsTrigger v-if="family.canManage" value="invitations"><AppIcon name="document" class="size-4" />Invitaciones</TabsTrigger></TabsList>
        <TabsContent value="members">
          <Card class="gap-0 overflow-hidden py-0">
            <CardHeader class="gap-4 border-b py-6 sm:flex sm:items-center sm:justify-between"><div><CardTitle><h2>Miembros del hogar</h2></CardTitle><CardDescription class="mt-1.5">{{ session?.household.name }}</CardDescription></div><div class="relative w-full sm:max-w-72"><Label for="family-search" class="sr-only">Buscar miembros</Label><AppIcon name="search" class="pointer-events-none absolute top-1/2 left-3.5 size-5 -translate-y-1/2 text-muted-foreground" /><Input id="family-search" v-model="search" type="search" autocomplete="off" maxlength="254" class="form-input pl-11" placeholder="Buscar una persona…" /></div></CardHeader>
            <CardContent class="p-0">
              <div class="hidden grid-cols-[minmax(0,1.5fr)_minmax(7rem,.65fr)_minmax(0,1fr)_48px] items-center gap-4 border-b bg-background/50 px-6 py-3 text-xs font-medium text-muted-foreground md:grid" aria-hidden="true"><span>Persona</span><span>Rol en el hogar</span><span>Tipo de cuenta</span><span class="sr-only">Opciones</span></div>
              <ul v-if="members.length" class="divide-y"><li v-for="member in members" :key="member.id" class="grid grid-cols-[minmax(0,1fr)_48px] items-center gap-4 px-5 py-5 md:grid-cols-[minmax(0,1.5fr)_minmax(7rem,.65fr)_minmax(0,1fr)_48px] md:px-6">
                <div class="flex min-w-0 items-start gap-3"><span aria-hidden="true" class="flex size-11 shrink-0 items-center justify-center rounded-full bg-secondary font-heading text-sm font-bold text-primary">{{ initials(member.name) }}</span><div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><h3 class="break-words text-sm font-semibold">{{ member.name }}</h3><span v-if="member.id === session?.user.id" class="text-xs text-muted-foreground">Tú</span></div><p v-if="member.username" class="mt-1 break-words text-xs leading-5 text-muted-foreground">@{{ member.username }}</p><p v-if="member.email" class="mt-0.5 break-all text-xs leading-5 text-muted-foreground">{{ member.email }}</p><div class="mt-2 flex flex-wrap gap-2 md:hidden"><Badge variant="secondary">{{ roleLabel(member.role) }}</Badge><span v-if="member.assisted" class="text-xs leading-6 text-muted-foreground">Acceso acompañado</span></div></div></div>
                <div class="hidden md:block"><Badge :variant="member.role === 'owner' ? 'secondary' : 'outline'">{{ roleLabel(member.role) }}</Badge></div>
                <p class="hidden text-sm leading-6 text-muted-foreground md:block">{{ member.assisted ? 'Acceso acompañado' : 'Cuenta personal' }}<span v-if="member.assisted" class="block text-xs">Preparada con ayuda</span></p>
                <DropdownMenu><DropdownMenuTrigger as-child><Button variant="ghost" size="icon" :id="'member-actions-' + member.id" :aria-label="'Opciones de ' + member.name" :disabled="busy"><svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" class="size-5"><circle cx="5" cy="12" r="1.7" /><circle cx="12" cy="12" r="1.7" /><circle cx="19" cy="12" r="1.7" /></svg></Button></DropdownMenuTrigger><DropdownMenuContent align="end" :side-offset="4" class="z-50 min-w-60 max-w-[90vw] rounded-xl border bg-popover p-1.5 shadow-lg">
                  <DropdownMenuItem class="nav-item cursor-pointer gap-3" @select="edit(member)"><AppIcon name="shield" class="size-4" />Ver permisos</DropdownMenuItem>
                  <template v-if="canEdit(member)"><DropdownMenuItem class="nav-item cursor-pointer gap-3" @select="edit(member, 'edit')"><AppIcon name="settings" class="size-4" />Editar rol y permisos</DropdownMenuItem><DropdownMenuSeparator class="my-1 border-t" /><DropdownMenuItem variant="destructive" @select="edit(member, 'remove')">Retirar del hogar</DropdownMenuItem></template>
                </DropdownMenuContent></DropdownMenu>
              </li></ul>
              <div v-else class="px-6 py-12 text-center"><AppIcon name="users" class="mx-auto size-9 text-muted-foreground" /><h3 class="mt-4 font-semibold">{{ search ? 'No encontramos a esa persona' : 'Aquí estarán los miembros de tu hogar' }}</h3><p class="mt-2 text-sm leading-6 text-muted-foreground">{{ search ? 'Prueba con otro nombre, usuario o correo.' : 'Invita a alguien para empezar a compartir.' }}</p><Button v-if="search" variant="outline" class="mt-5" @click="search = ''">Ver a todos</Button></div>
            </CardContent>
            <div class="flex flex-wrap items-center justify-between gap-2 border-t bg-background/40 px-6 py-3 text-xs leading-6 text-muted-foreground"><p>{{ search ? members.length + ' de ' : '' }}{{ family.members.length }} {{ family.members.length === 1 ? 'persona en tu hogar' : 'personas en tu hogar' }}</p><p>Los permisos se consultan en el menú de cada persona.</p></div>
          </Card>
        </TabsContent>
        <TabsContent v-if="family.canManage" value="invitations">
          <Card><CardHeader><CardTitle><h2>Invitaciones recientes</h2></CardTitle><CardDescription>Consulta las últimas 100 invitaciones y retira los enlaces que ya no necesites.</CardDescription></CardHeader><CardContent>
            <div v-if="invitationsError" role="alert" class="rounded-xl bg-danger-muted p-4"><p class="text-sm text-destructive">{{ invitationsError }}</p><Button variant="outline" class="mt-3" :disabled="loading" @click="load">Reintentar</Button></div>
            <div v-else-if="!invitations.length" class="py-8 text-center"><span class="mx-auto flex size-14 items-center justify-center rounded-2xl bg-secondary text-primary"><AppIcon name="users" /></span><h3 class="mt-4 font-semibold">Hay sitio para los tuyos</h3><p class="mx-auto mt-2 max-w-sm text-sm leading-7 text-muted-foreground">Cuando invites a alguien, aquí podrás consultar su invitación. El enlace es privado y dura 72 horas.</p><Button class="mt-5" @click="beginInvite">Invitar a alguien</Button></div>
            <ul v-else class="divide-y"><li v-for="invitation in invitations" :key="invitation.id" class="flex flex-wrap items-center justify-between gap-4 py-5"><div class="min-w-0 flex-1"><p class="break-all text-sm font-semibold text-heading">{{ invitation.email }}</p><div class="mt-2 flex flex-wrap items-center gap-2"><Badge :variant="invitation.status === 'pending' ? 'secondary' : 'outline'">{{ statusLabel(invitation.status) }}</Badge><span class="text-xs text-muted-foreground">{{ roleLabel(invitation.role) }}</span></div><p class="mt-2 text-xs leading-6 text-muted-foreground">Caduca {{ formatHouseholdDate(invitation.expiresAt, true) }}</p></div><Button v-if="invitation.status === 'pending' && (session?.role === 'owner' || invitation.role !== 'admin')" variant="outline" size="sm" :disabled="busy" :aria-label="'Revocar invitación para ' + invitation.email" @click="error = ''; revoking = invitation">Revocar</Button></li></ul>
          </CardContent></Card>
        </TabsContent>
      </Tabs>
      <aside class="flex items-start gap-3 rounded-2xl border border-primary/10 bg-secondary/50 p-5"><AppIcon name="shield" class="mt-0.5 size-5 text-primary" /><div><h2 class="text-sm font-semibold">Compartir con tranquilidad</h2><p class="mt-1 text-sm leading-7 text-muted-foreground">Ser miembro o administrador no permite ver los documentos privados de otra persona. Su propietario decide con quién compartirlos.</p></div></aside>
      <Button v-if="session?.user.email" variant="link" class="px-0" @click="emailOpen = true">Mi correo y recuperación de acceso<AppIcon name="arrow" class="size-4" /></Button>
    </template>

    <ActionDialog v-if="inviteOpen && family?.canManage" title="Invitar a alguien al hogar" :description="inviteURL ? 'La invitación está lista. Copia su enlace antes de cerrar.' : 'Elige cómo participará esta persona en tu hogar.'" :busy="busy" @close="closeInvite">
      <div v-if="inviteURL" class="space-y-5"><Alert role="status" class="rounded-xl bg-secondary p-4 text-sm leading-7"><AlertDescription class="text-inherit leading-7">{{ inviteNotice }}</AlertDescription></Alert><div class="space-y-2"><Label for="invitation-link" class="form-label">Enlace privado de invitación</Label><Input id="invitation-link" :model-value="inviteURL" readonly class="form-input" @focus="($event.target as HTMLInputElement).select()" /></div><p class="text-sm leading-7 text-muted-foreground">Este enlace solo se muestra ahora. Compártelo en privado con la persona invitada; caduca en 72 horas y se puede usar una sola vez.</p><div class="flex flex-wrap gap-3"><Button @click="copy">Copiar enlace</Button><Button variant="outline" @click="closeInvite">Terminar</Button></div></div>
      <form v-else id="household-invitation-form" class="space-y-5" :aria-busy="busy" @submit.prevent="invite">
        <fieldset class="space-y-5" :disabled="busy">
          <div class="space-y-2"><Label for="invite-email">Correo electrónico</Label><Input id="invite-email" v-model.trim="email" type="email" maxlength="254" autocomplete="off" required placeholder="persona@ejemplo.com" /></div>
          <GrantFields v-model:role="role" v-model:permissions="permissions" :roles="family.roles" :capabilities="family.capabilities" prefix="invite" />
          <Label v-if="smtpEnabled" for="invite-send-email" class="form-option grid font-normal"><Checkbox id="invite-send-email" v-model="sendEmail" /><span>Enviar también por correo electrónico</span></Label>
          <div class="space-y-1 text-xs leading-5 text-muted-foreground"><p v-if="!smtpEnabled">Correo desactivado. Podrás copiar y compartir el enlace.</p><p>Enlace privado · 72 horas · Un uso.</p><p>Otra invitación al mismo correo reemplaza la anterior.</p></div>
          <Alert v-if="inviteError" role="alert" variant="destructive"><AlertDescription class="text-sm leading-6">{{ inviteError }}</AlertDescription></Alert>
        </fieldset>
      </form>
      <template v-if="!inviteURL" #footer><div class="flex flex-wrap justify-end gap-3"><Button type="button" variant="outline" :disabled="busy" @click="closeInvite">Cancelar</Button><Button type="submit" form="household-invitation-form" :disabled="busy">{{ busy ? 'Creando…' : 'Crear invitación' }}</Button></div></template>
    </ActionDialog>
    <ActionDialog v-if="selected" :return-focus="returnMemberFocus" :title="memberAction === 'remove' ? 'Retirar del hogar' : memberAction === 'edit' ? 'Editar rol y permisos' : 'Permisos en el hogar'" :description="selected.name" :busy="busy" @close="selected = undefined">
      <div v-if="memberAction === 'view'" class="space-y-6"><Badge variant="secondary">{{ roleLabel(selected.role) }}</Badge><ul class="space-y-3"><li v-for="permission in selected.permissions" :key="permission" class="flex items-start gap-3 rounded-xl border p-4 text-sm leading-7"><AppIcon name="shield" class="mt-1 size-4 text-primary" />{{ permissionLabel(permission) }}</li><li v-if="!selected.permissions.length" class="text-sm leading-7 text-muted-foreground">Sin permisos de Documents.</li></ul><p v-if="selected.role === 'owner'" class="text-sm leading-7 text-muted-foreground">El propietario está protegido: su rol no puede cambiarse ni puede retirarse desde esta pantalla.</p><div class="flex flex-wrap gap-3"><Button variant="outline" @click="selected = undefined">Cerrar</Button><Button v-if="canEdit(selected)" @click="memberAction = 'edit'">Editar permisos</Button></div></div>
      <form v-else class="space-y-6" :aria-busy="busy" @submit.prevent="saveMember"><fieldset class="space-y-5" :disabled="busy"><p v-if="memberAction === 'remove'" class="rounded-xl bg-danger-muted p-4 text-sm leading-7">Esta persona perderá sus sesiones y acceso a este hogar. Sus documentos se conservan. Si vuelve mediante otra invitación, los accesos compartidos anteriores no se reactivarán.</p><GrantFields v-else v-model:role="editRole" v-model:permissions="editPermissions" :roles="family?.roles ?? []" :capabilities="family?.capabilities ?? []" prefix="edit-member" /><Alert v-if="error" role="alert" class="text-sm leading-7 text-destructive" variant="destructive"><AlertDescription class="text-inherit leading-7">{{ error }}</AlertDescription></Alert><div class="flex flex-wrap gap-3"><Button type="button" variant="outline" @click="selected = undefined">Cancelar</Button><Button type="submit" :variant="memberAction === 'remove' ? 'destructive' : 'default'">{{ busy ? 'Guardando…' : memberAction === 'remove' ? 'Retirar del hogar' : 'Guardar permisos' }}</Button></div></fieldset></form>
    </ActionDialog>
    <ActionDialog v-if="revoking" title="Revocar invitación" :description="revoking.email" :busy="busy" @close="revoking = undefined"><form class="space-y-6" @submit.prevent="revoke"><p class="text-sm leading-7 text-muted-foreground">El enlace dejará de funcionar. Si esta persona aún necesita entrar, podrás preparar una nueva invitación.</p><Alert v-if="error" role="alert" class="text-sm text-destructive" variant="destructive"><AlertDescription class="text-inherit leading-7">{{ error }}</AlertDescription></Alert><div class="flex flex-wrap gap-3"><Button type="button" variant="outline" :disabled="busy" @click="revoking = undefined">Cancelar</Button><Button variant="destructive" type="submit" :disabled="busy">{{ busy ? 'Revocando…' : 'Revocar invitación' }}</Button></div></form></ActionDialog>
    <ActionDialog v-if="emailOpen" title="Mi correo y recuperación" description="El correo es opcional. Tu acceso local sigue disponible." @close="emailOpen = false"><EmailVerificationCard class="border-0 p-0 shadow-none" /></ActionDialog>
  </section>
</template>
