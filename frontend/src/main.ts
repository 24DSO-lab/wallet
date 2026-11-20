import { createPinia } from 'pinia'
import { createApp } from 'vue'
import App from './App.vue'
import { i18n } from './i18n'
import { router } from './router'
import { useSessionStore } from './stores/session'
import './style.css'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia).use(i18n)

// Resolve the session before the first navigation so the guard can route a
// locked wallet straight to the lock screen without a flash of other content.
await useSessionStore(pinia).init()
app.use(router)
await router.isReady()
app.mount('#app')
