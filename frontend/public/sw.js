// Offline shell: the app opens on the street with no signal. Records wait in
// IndexedDB (src/outbox.ts) until the server is reachable; /api is never cached.
const CACHE = 'bairro-em-acao-v2'

async function precache() {
  const cache = await caches.open(CACHE)
  const shell = await fetch('/', { cache: 'no-cache' })
  const html = await shell.clone().text()
  const assets = [...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)].map((m) => m[1])
  await cache.put('/', shell)
  await cache.addAll([...new Set([...assets, '/icon.svg', '/manifest.webmanifest'])])
}

self.addEventListener('install', (event) => {
  event.waitUntil(precache().then(() => self.skipWaiting()))
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

// Weak signal can hang a request for a long time; fall back to the cache quickly.
function withTimeout(promise, ms) {
  return Promise.race([promise, new Promise((_, reject) => setTimeout(() => reject(new Error('timeout')), ms))])
}

self.addEventListener('fetch', (event) => {
  const { request } = event
  const url = new URL(request.url)
  if (request.method !== 'GET' || url.origin !== self.location.origin || url.pathname.startsWith('/api/')) return
  if (request.mode === 'navigate') {
    event.respondWith(
      withTimeout(fetch(request), 3000)
        .then((res) => {
          if (res.ok) {
            const copy = res.clone()
            caches.open(CACHE).then((c) => c.put('/', copy))
          }
          return res
        })
        .catch(() => caches.match('/', { ignoreVary: true })),
    )
    return
  }
  // Build assets have content hashes, so a cached copy is always correct. Module
  // scripts send Origin and servers answer Vary: Origin; the variant is irrelevant here.
  event.respondWith(
    caches.match(request, { ignoreVary: true }).then(
      (hit) =>
        hit ||
        fetch(request).then((res) => {
          if (res.ok && url.pathname.startsWith('/assets/')) {
            const copy = res.clone()
            caches.open(CACHE).then((c) => c.put(request, copy))
          }
          return res
        }),
    ),
  )
})
