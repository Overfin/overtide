import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The panel is built into the Go package that serves it (internal/httpui),
// which embeds it: `go build` needs no Node.
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/httpui/dist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
    sourcemap: false,
    // Characters outside ASCII written as escapes, as in Overfin's panel.
    rolldownOptions: { output: { charset: 'ascii' } },
  },
  server: {
    // npm run dev: the API is Overtide's, running on its usual port.
    proxy: { '/api': 'http://localhost:8089', '/ui': 'http://localhost:8089' },
  },
});
