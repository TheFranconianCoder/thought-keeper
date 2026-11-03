import { mount } from 'svelte';
import './styles/app.css';
import App from './App.svelte';
import { freezeDocumentScroll, trackViewportHeight } from './lib/viewport';
import { trackWindowControls } from './lib/window-controls';
import { initTheme } from './lib/theme.svelte';

const target = document.getElementById('app');
if (!target) throw new Error('#app is missing');

// The installed PWA uses the platform's in-window controls. Apply the geometry before the first
// render, so the top bar never paints over them and then jumps aside.
trackWindowControls();
// The shell is sized to the visible viewport, and the document never scrolls. Both are set before
// the first render: a shell that starts with the wrong height is what puts the footer off-screen.
trackViewportHeight();
freezeDocumentScroll();
initTheme();

// The service worker makes the PWA installable and lets the shell open when the local service is not
// running. The API is deliberately never cached: a cached answer about the vault would be a lie at
// exactly the moment the reader cannot check it.
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    void navigator.serviceWorker.register('/sw.js').catch(() => {
      /* installability is a nice-to-have; never block the app */
    });
  });
}

export default mount(App, { target });
