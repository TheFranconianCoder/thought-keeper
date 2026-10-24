import { mount } from 'svelte';
import './styles/app.css';
import App from './App.svelte';
import { trackWindowControls } from './lib/windowControls';

const target = document.getElementById('app');
if (!target) throw new Error('#app missing');

// The installed PWA runs with the platform's window controls drawn INSIDE the window (the manifest
// asks for `window-controls-overlay`). This is what tells the layout whether they are really there —
// before the first render, so the bar never paints over them and then jumps aside.
trackWindowControls();

// The service worker is only needed in the built app. It makes the PWA installable (a real app window)
// and it is what lets the installed app OPEN WITHOUT THE SERVICE: the shell — the document, its hashed
// assets, the manifest, the icons — is cached (web/public/sw.js), so the window comes up and says
// "off" instead of showing nothing. The API is never cached: this app IS the local service, and a cached
// answer about the vault would be a lie told at exactly the moment the reader cannot check it.
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
	window.addEventListener('load', () => {
		navigator.serviceWorker.register('/sw.js').catch(() => {
			/* installability is a nice-to-have; never block the app */
		});
	});
}

export default mount(App, { target });
