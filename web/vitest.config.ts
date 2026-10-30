import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  // The Svelte plugin is what compiles `.svelte.ts` rune modules. Without it a store imported by a
  // test (`$state`) fails at module evaluation, not in the test.
  plugins: [svelte()],
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
