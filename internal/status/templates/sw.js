// radio-dj service worker — app-shell cache for installability + offline UI.
// The stream itself can't be cached (live audio); this only caches the page
// shell so the install prompt fires and the app opens offline.
const CACHE = 'radio-dj-v7';
const SHELL = ['/', '/manifest.json', '/icon-192.png', '/icon-512.png', '/font/permanent-marker.woff2'];

self.addEventListener('install', e => {
  e.waitUntil(caches.open(CACHE).then(c => c.addAll(SHELL)).catch(() => {}).then(() => self.skipWaiting()));
});

self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))).then(() => self.clients.claim()));
});

// network-first for the page (fresh HTML), cache fallback offline; cache-first for static assets.
self.addEventListener('fetch', e => {
  const url = new URL(e.request.url);
  if (e.request.method !== 'GET' || url.origin !== self.location.origin) return;
  if (url.pathname === '/' ) {
    e.respondWith(fetch(e.request).catch(() => caches.match('/')));
  } else if (SHELL.includes(url.pathname)) {
    e.respondWith(caches.match(e.request).then(r => r || fetch(e.request)));
  }
});
