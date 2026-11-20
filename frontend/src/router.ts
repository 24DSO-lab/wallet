import { createRouter, createWebHashHistory } from 'vue-router'
import { useSendStore } from './stores/send'
import { useSessionStore } from './stores/session'
import { useUiStore } from './stores/ui'

declare module 'vue-router' {
  interface RouteMeta {
    /** i18n key of the page title shown in the shell header. */
    title?: string
    /** Rendered without the application shell (lock screen). */
    bare?: boolean
  }
}

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/lock', name: 'lock', component: () => import('./views/LockView.vue'), meta: { bare: true } },
    { path: '/', name: 'home', component: () => import('./views/HomeView.vue'), meta: { title: 'title.home' } },
    { path: '/wizard', name: 'wizard', component: () => import('./views/WizardView.vue'), meta: { title: 'title.wizard' } },
    {
      path: '/import-check',
      name: 'import-check',
      component: () => import('./views/ImportCheckView.vue'),
      meta: { title: 'title.wizard' },
    },
    {
      path: '/wallet/:id',
      name: 'wallet',
      component: () => import('./views/WalletView.vue'),
      props: true,
      meta: { title: 'title.wallet' },
    },
    {
      path: '/receive/:id/:asset',
      name: 'receive',
      component: () => import('./views/ReceiveView.vue'),
      props: true,
      meta: { title: 'title.receive' },
    },
    { path: '/send', name: 'send', component: () => import('./views/SendView.vue'), meta: { title: 'title.send' } },
    {
      path: '/review',
      name: 'review',
      component: () => import('./views/ReviewView.vue'),
      meta: { title: 'title.send' },
      beforeEnter: () => (useSendStore().quote ? true : { name: 'send' }),
    },
    {
      path: '/confirm',
      name: 'confirm',
      component: () => import('./views/ConfirmView.vue'),
      meta: { title: 'title.send' },
      beforeEnter: () => (useSendStore().quote ? true : { name: 'send' }),
    },
    { path: '/history', name: 'history', component: () => import('./views/HistoryView.vue'), meta: { title: 'title.history' } },
    { path: '/settings', name: 'settings', component: () => import('./views/SettingsView.vue'), meta: { title: 'title.settings' } },
    { path: '/:pathMatch(.*)*', redirect: { name: 'home' } },
  ],
})

// A locked session may only show the lock screen; an unlocked one never does.
router.beforeEach(to => {
  const session = useSessionStore()
  useUiStore().clear()
  if (session.locked && to.name !== 'lock') return { name: 'lock' }
  if (!session.locked && to.name === 'lock') return { name: 'home' }
  return true
})
