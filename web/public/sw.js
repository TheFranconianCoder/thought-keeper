// Thought Keeper service worker.
//
// It exists so the app can be INSTALLED (Chrome/Edge require a fetch handler) and so the installed app
// OPENS without the service: the app itself — its document, its hashed assets, its manifest, its icons —
// is cached, and nothing else.
//
// What is deliberately NOT cached is the API. This app IS a local service, so "offline" means the app is
// not running; a cached answer to "what is in my vault?" would be a lie told at exactly the moment the
// reader cannot check it. The UI says `off` instead, which is true.
//
// Bump CACHE whenever a PRECACHED file changes (the icons, the manifest): precached entries are served
// cache-first, so a new icon would otherwise only appear after the cache is cleared. A new BUILD needs no
// bump — the document is network-first and its hashed assets are cached under names that change with the
// build.
const CACHE = 'thought-keeper-v2';

// SHELL is the part of the app that must be there before anything is asked of the network: the document
// itself, the manifest, and the icons the platform and the tab use. The hashed /assets/* files are NOT
// listed — their names change with every build — they are discovered from the document at install and
// cached as the page loads them.
const SHELL = [
	'/',
	'/manifest.webmanifest',
	'/icon.svg',
	'/icon-192.png',
	'/icon-512.png',
	'/icon-maskable-512.png',
	'/apple-touch-icon.png',
];

// networkFirst serves the fresh copy when the service answers and keeps the cached one current, so the
// offline copy is the one the reader last saw. The fallback is what makes the app open at all.
async function networkFirst(req, key) {
	const cache = await caches.open(CACHE);
	try {
		const res = await fetch(req);
		if (res.ok && res.type === 'basic') await cache.put(key, res.clone());
		return res;
	} catch (err) {
		const hit = await cache.match(key);
		if (hit) return hit;
		throw err;
	}
}

// cacheFirst is for what never changes under its name: the hashed assets, the icons.
async function cacheFirst(req) {
	const cache = await caches.open(CACHE);
	const hit = await cache.match(req);
	if (hit) return hit;
	const res = await fetch(req);
	if (res.ok && res.type === 'basic') await cache.put(req, res.clone());
	return res;
}

// precache puts the shell in the cache — and with it the assets the document references. Without those
// the offline app would open its HTML and then fail on its own JavaScript: a blank page is not an app
// that works offline. The names are read from the built document, because that is the only place they
// are known.
async function precache() {
	const cache = await caches.open(CACHE);
	for (const path of SHELL) {
		try {
			const res = await fetch(path, { cache: 'no-cache' });
			if (res.ok && res.type === 'basic') await cache.put(path, res.clone());
		} catch (err) {
			// One missing file (an icon a platform does not ask for) must not cost the whole shell.
		}
	}
	try {
		const doc = await cache.match('/');
		if (!doc) return;
		const html = await doc.text();
		const assets = new Set(html.match(/\/assets\/[A-Za-z0-9._-]+/g) ?? []);
		for (const path of assets) {
			try {
				const res = await fetch(path, { cache: 'no-cache' });
				if (res.ok && res.type === 'basic') await cache.put(path, res.clone());
			} catch (err) {
				/* the runtime cache-first handler picks it up on the next load */
			}
		}
	} catch (err) {
		/* a document that cannot be read is handled by the network-first path */
	}
}

self.addEventListener('install', (event) => {
	// The shell is fetched NOW, not at the first offline attempt: an app that can only be installed while
	// online should be usable the moment it is installed.
	event.waitUntil(
		(async () => {
			await precache();
			await self.skipWaiting();
		})(),
	);
});

self.addEventListener('activate', (event) => {
	event.waitUntil(
		(async () => {
			for (const key of await caches.keys()) {
				if (key !== CACHE) await caches.delete(key);
			}
			await self.clients.claim();
		})(),
	);
});

self.addEventListener('fetch', (event) => {
	const req = event.request;
	if (req.method !== 'GET') return;

	const url = new URL(req.url);
	if (url.origin !== self.location.origin) return;
	// The API is always live, the event stream included.
	if (url.pathname.startsWith('/api/')) return;
	// A cached worker is a worker that can never be replaced.
	if (url.pathname === '/sw.js') return;

	// The document: fresh when the service answers, the cached one when it does not. This is what makes
	// the installed app open offline.
	if (req.mode === 'navigate') {
		event.respondWith(networkFirst(req, '/'));
		return;
	}
	// The manifest can change without a build, so it is revalidated like the document.
	if (url.pathname === '/manifest.webmanifest') {
		event.respondWith(networkFirst(req, url.pathname));
		return;
	}
	// Everything the app is made of, under a name that changes with the build.
	if (url.pathname.startsWith('/assets/') || SHELL.includes(url.pathname)) {
		event.respondWith(cacheFirst(req));
		return;
	}
});
