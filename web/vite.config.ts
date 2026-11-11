import { resolve } from 'node:path';
import { defineConfig } from 'vite';

export default defineConfig({
  server: { host: '127.0.0.1', proxy: { '/api': 'http://127.0.0.1:8080' } },
  build: {
    outDir: 'dist',
    rollupOptions: {
      // Two separate pages, so the portal never loads Phaser and the game never loads React.
      input: { main: resolve(import.meta.dirname, 'index.html'), admin: resolve(import.meta.dirname, 'admin.html') },
    },
  },
});
