import { useSyncExternalStore } from 'react'

export type Route = { name: 'home' } | { name: 'capture' } | { name: 'walk'; id: string }

function parse(hash: string): Route {
  const walk = hash.match(/^#\/caminhadas\/([0-9a-f-]{36})$/)
  if (walk) return { name: 'walk', id: walk[1] }
  if (hash === '#/registrar') return { name: 'capture' }
  return { name: 'home' }
}

function subscribe(fn: () => void) {
  window.addEventListener('hashchange', fn)
  return () => window.removeEventListener('hashchange', fn)
}

export function useRoute(): Route {
  const hash = useSyncExternalStore(subscribe, () => window.location.hash)
  return parse(hash)
}

export function navigate(hash: string, { replace = false } = {}): void {
  if (replace) {
    history.replaceState(null, '', hash)
    window.dispatchEvent(new HashChangeEvent('hashchange'))
  } else {
    window.location.hash = hash
  }
}
