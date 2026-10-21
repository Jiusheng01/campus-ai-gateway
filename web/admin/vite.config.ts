import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  base: '/admin-ui/',
  plugins: [react()],
  build: {
    outDir: fileURLToPath(new URL('../../internal/app/adminui/dist', import.meta.url)),
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      // changeOrigin must stay false. The gateway's same-origin check compares
      // the browser's Origin header with the Host it receives; rewriting Host
      // to the target (Vite's default for a string shorthand) made /admin
      // look cross-site, so the refresh call was refused and `npm run dev`
      // dropped to the login screen on every reload.
      // Match API paths without intercepting the /admin-ui/ frontend assets.
      '^/admin(?:/|$)': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '^/auth(?:/|$)': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/api/console': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/metrics': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/healthz': { target: 'http://127.0.0.1:8080', changeOrigin: false },
      '/readyz': { target: 'http://127.0.0.1:8080', changeOrigin: false },
    },
  },
})
