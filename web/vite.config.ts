import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
      '/v1': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
  build: {
    outDir: resolve(__dirname, 'dist'),
    emptyOutDir: true,
    // 将大型 UI/图表依赖拆开，降低 Rollup rendering chunks 阶段的内存峰值。
    rollupOptions: {
      output: {
        manualChunks: {
          'vendor-vue': ['vue', 'vue-router', 'pinia'],
          'vendor-ui': ['naive-ui', '@vicons/ionicons5'],
          'vendor-charts': ['echarts', 'vue-echarts', '@silverwind/vue3-calendar-heatmap'],
          'vendor-utils': ['axios', '@vueuse/core', 'tippy.js'],
        },
      },
    },
  },
})
