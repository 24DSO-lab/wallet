import { createI18n } from 'vue-i18n'
export const i18n = createI18n({ legacy: false, locale: 'ru', fallbackLocale: 'ru', messages: { ru: { appName: 'Wallet', dashboard: 'Обзор', history: 'История', settings: 'Настройки' }, en: { appName: 'Wallet', dashboard: 'Overview', history: 'History', settings: 'Settings' } } })
