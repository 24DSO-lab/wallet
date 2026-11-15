import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
export default defineConfig({ plugins:[vue()], base:'./', server:{host:'127.0.0.1'}, test:{environment:'jsdom',globals:true,include:['src/**/*.spec.ts']} })