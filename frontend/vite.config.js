import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
        // 去掉 /api 前缀：/api/user/login -> http://127.0.0.1:8080/user/login
        rewrite: (path) => path.replace(/^\/api/, '')
      }
    }
  }
})
