import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { mkdirSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'

function keepDistGitkeep() {
  return {
    name: 'sideplane-dist-gitkeep',
    closeBundle() {
      const distDir = resolve(__dirname, 'dist')
      mkdirSync(distDir, { recursive: true })
      writeFileSync(resolve(distDir, '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  base: './',
  plugins: [react(), keepDistGitkeep()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
      '/readyz': 'http://localhost:8080',
      '/metrics': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
