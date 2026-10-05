import basicSsl from '@vitejs/plugin-basic-ssl'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// `--mode phone` serves HTTPS on the LAN with a self-signed certificate: phones
// only allow location, the offline cache and crypto.randomUUID on secure origins.
export default defineConfig(({ mode }) => {
  const api = loadEnv(mode, '.', '').API_TARGET || 'http://127.0.0.1:8090'
  return {
    plugins: [react(), ...(mode === 'phone' ? [basicSsl()] : [])],
    server: { port: 5174, strictPort: true, proxy: { '/api': api } },
    preview: { port: 4174, strictPort: true, proxy: { '/api': api } },
  }
})
