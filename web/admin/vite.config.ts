import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { resolve } from 'node:path'

export default defineConfig({
  base: '/admin-ui/',
  plugins: [react()],
  build: {
    outDir: resolve(__dirname, '../../internal/app/adminui/dist'),
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      '/admin': 'http://127.0.0.1:8080',
      '/metrics': 'http://127.0.0.1:8080',
      '/healthz': 'http://127.0.0.1:8080',
      '/readyz': 'http://127.0.0.1:8080',
    },
  },
})
