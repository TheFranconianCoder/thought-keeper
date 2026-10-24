// Checks the service worker's RULES, not its syntax: what it caches, what it must never cache, and
// whether the installed app really opens without the service.
//
// It runs without a browser — `caches`, `fetch` and `self` are stubbed — because the rules are the part
// that breaks silently: caching the API, or forgetting to precache the assets a document references,
// would look fine in every manual test that happens to be online.
//
//   node scripts/check-sw.mjs
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(join(here, '..', 'public', 'sw.js'), 'utf8');

// A document with the shape the build produces: hashed asset references.
const HTML = [
	'<!doctype html><html><head>',
	'<script type="module" crossorigin src="/assets/index-ABCDEF12.js"></script>',
	'<link rel="stylesheet" crossorigin href="/assets/index-ZYXWVU98.css">',
	'</head><body><div id="app"></div></body></html>',
].join('');
const ORIGIN = 'http://127.0.0.1:9210';

const store = new Map();
// A real Cache keys by the ABSOLUTE url: a relative path is resolved on put, and match(request)
// compares the same url. A stub that keys naively would test itself instead of the worker.
const key = (k) => new URL(typeof k === 'string' ? k : k.url, ORIGIN).href;
const cache = {
	put: async (k, r) => {
		store.set(key(k), r);
	},
	match: async (k) => store.get(key(k)),
};

const handlers = {};
globalThis.caches = {
	open: async () => cache,
	keys: async () => ['thought-keeper-v3'],
	delete: async () => true,
};
globalThis.self = {
	location: { origin: ORIGIN },
	skipWaiting: async () => {},
	clients: { claim: async () => {} },
	addEventListener: (type, fn) => {
		handlers[type] = fn;
	},
};

let online = true;
let fetched = [];
const response = (body, url) => ({
	ok: true,
	type: 'basic',
	url,
	clone: () => response(body, url),
	text: async () => body,
});
globalThis.fetch = async (req) => {
	const url = typeof req === 'string' ? req : req.url;
	fetched.push(url);
	if (!online) throw new Error('offline');
	return response(url === '/' ? HTML : `/* ${url} */`, url);
};

new Function('self', 'caches', 'fetch', source)(globalThis.self, globalThis.caches, globalThis.fetch);

const failures = [];
const check = async (label, fn) => {
	try {
		await fn();
		console.log('  ok  ', label);
	} catch (err) {
		failures.push(`${label}: ${err.message}`);
		console.log('  FAIL', label, '—', err.message);
	}
};
const expect = (condition, message) => {
	if (!condition) throw new Error(message);
};

// install precaches the shell.
const waits = [];
handlers.install({ waitUntil: (p) => waits.push(p) });
await Promise.all(waits);
await check('install caches the document', async () => expect(await cache.match('/'), 'not cached'));
await check('install caches the icons', async () => expect(await cache.match('/icon-512.png'), 'not cached'));
await check('install discovers the hashed assets the document references', async () => {
	const assets = [...store.keys()].filter((k) => k.includes('/assets/'));
	expect(assets.length >= 2, `found ${assets.length} assets`);
});

// Offline: a navigation is answered from the cache, with the app itself.
online = false;
let navigation = null;
handlers.fetch({
	request: { method: 'GET', url: `${ORIGIN}/`, mode: 'navigate' },
	respondWith: (p) => {
		navigation = p;
	},
});
await check('an offline navigation is answered', async () => expect(navigation, 'no respondWith'));
if (navigation) {
	const body = await (await navigation).text();
	await check('an offline navigation serves the app', async () => expect(body === HTML, 'not the document'));
}

// The API is never touched, and never stored.
for (const path of ['/api/v1/notes', '/api/v1/events', '/api/v1/pipeline']) {
	let intercepted = null;
	handlers.fetch({
		request: { method: 'GET', url: ORIGIN + path, mode: 'cors' },
		respondWith: (p) => {
			intercepted = p;
		},
	});
	await check(`${path} stays live`, async () => expect(intercepted === null, 'the worker stepped in'));
}
await check('no API path is ever cached', async () =>
	expect(
		[...store.keys()].every((k) => !new URL(k).pathname.startsWith('/api/')),
		'an API path was cached',
	));

// The app's own files come from the cache, without the network.
fetched = [];
const asset = new URL([...store.keys()].find((k) => k.includes('/assets/'))).pathname;
let assetAnswer = null;
handlers.fetch({
	request: { method: 'GET', url: ORIGIN + asset, mode: 'no-cors' },
	respondWith: (p) => {
		assetAnswer = p;
	},
});
if (assetAnswer) await assetAnswer;
await check('an asset is served from the cache', async () =>
	expect(fetched.length === 0, `fetched anyway: ${fetched.join(', ')}`),
);

if (failures.length) {
	console.error(`\n${failures.length} service-worker rule(s) broken:\n${failures.join('\n')}`);
	process.exit(1);
}
console.log('\nthe service worker keeps its rules');
