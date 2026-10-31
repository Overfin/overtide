// Svelte's own settings: TypeScript in components is handled by Vite.
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

export default {
  preprocess: vitePreprocess(),
};
