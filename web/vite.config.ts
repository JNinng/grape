/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// dev.proxy：开发期前后端分离，业务 API（/v1）与 AG-UI / ack（/agui，SSE
// 长流）代理到本机 Go 服务（地址见 configs/config.yaml 的 server.http.addr）；
// 生产期前后端同源、由 Go 静态托管，无需代理。
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/v1': 'http://127.0.0.1:8000',
      '/agui': 'http://127.0.0.1:8000',
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
  },
})
