import type { IconName } from '@/components/AppIcon.vue'

export interface HomeItem { id: string; title: string; description: string; to: string; date?: string }
export interface HomeSuggestion { label: string; term: string; icon: IconName; tone: 'blue' | 'amber' | 'warm' }
// Contributions contain text and local routes, never module HTML or arbitrary scripts.
export interface HomeContribution {
  caption: string
  recentCaption: string
  recentTitle: string
  loadingText: string
  icon: IconName
  image: string
  tone: 'blue' | 'amber' | 'warm'
  collection: { label: string; to: string }
  empty: { title: string; description: string; to: string }
  suggestions?: HomeSuggestion[]
  space: { description: string }
  load(): Promise<HomeItem[]>
}

export function moduleLink(path: string, base: string) {
  if (!path.startsWith('/') || path.startsWith('//') || /[\\\u0000-\u001f\u007f]/.test(path)) return false
  try {
    const url = new URL(path, 'https://homeatlas.invalid')
    return url.origin === 'https://homeatlas.invalid' && (url.pathname === base || url.pathname.startsWith(base + '/'))
  } catch { return false }
}
