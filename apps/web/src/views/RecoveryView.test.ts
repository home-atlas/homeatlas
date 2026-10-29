import { afterEach, describe, expect, it } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAuthState } from '@/core/auth'
import RecoveryView from './RecoveryView.vue'

async function renderRecovery(smtpEnabled: boolean, hash = '') {
  setAuthState({ setupRequired: false, session: null, smtpEnabled })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/forgot-password', component: RecoveryView }],
  })
  await router.push('/forgot-password' + hash)
  return renderToString(createSSRApp(RecoveryView).use(router))
}

afterEach(() => setAuthState({ setupRequired: false, session: null }))

describe('password recovery entry points', () => {
  it('offers local code recovery when SMTP is disabled', async () => {
    const html = await renderRecovery(false)
    expect(html).toContain('id="recovery-code"')
    expect(html).toContain('id="recovery-password"')
    expect(html).not.toContain('id="recovery-email"')
  })

  it('offers verified email recovery and keeps the code option available with SMTP', async () => {
    const html = await renderRecovery(true)
    expect(html).toContain('id="recovery-email"')
    expect(html).toContain('Ya tengo un código de recuperación')
    expect(html).not.toContain('id="recovery-password"')
  })

  it('opens code entry directly for a recovery link even with SMTP enabled', async () => {
    const html = await renderRecovery(true, '#test-only-code')
    expect(html).toContain('value="test-only-code"')
    expect(html).toContain('id="recovery-code"')
    expect(html).not.toContain('id="recovery-email"')
  })
})
