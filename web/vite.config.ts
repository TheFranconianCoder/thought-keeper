import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The PWA. Its built assets land directly in the Go embed directory, so `go build` embeds exactly
// what Vite produced. In dev, /api is proxied to the running Go app.
const apiTarget = 'http://127.0.0.1:9210';

// buildManifest writes the one fact an open window needs to notice that the app moved on: the entry
// chunk of the build it is serving. The document is network-first, but a window that is already
// open never asks for it again, and the service worker changes no file between builds — so without
// this file an installed app would keep the bundle it loaded until the reader forces a restart.
// The Go handler revalidates everything that is not a hashed asset (`no-cache`), so the manifest
// cannot go stale.
function buildManifest(): Plugin {
  return {
    name: 'thought-keeper-build-manifest',
    apply: 'build',
    generateBundle(_options, bundle) {
      const entry = Object.values(bundle).find((file) => file.type === 'chunk' && file.isEntry);
      if (!entry || entry.type !== 'chunk') return;
      this.emitFile({
        type: 'asset',
        fileName: 'build.json',
        source: `${JSON.stringify({ entry: `/${entry.fileName}` })}\n`,
      });
    },
  };
}

export default defineConfig({
  plugins: [svelte(), buildManifest()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    target: 'es2022',
    sourcemap: false,
  },
  server: {
    port: 5174,
    strictPort: true,
    proxy: {
      '/api': apiTarget,
    },
  },
});
