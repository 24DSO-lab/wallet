import { createI18n } from 'vue-i18n'
import { en } from './en'
import { ru } from './ru'

/**
 * Russian plural rule for messages written as "zero | one | few | many".
 * Used by `t(key, count)`, for example the auto-lock options.
 */
function russianPlural(choice: number, choicesLength: number): number {
  if (choice === 0) return 0
  const teen = choice > 10 && choice < 20
  const endsWithOne = choice % 10 === 1
  if (!teen && endsWithOne) return 1
  if (!teen && choice % 10 >= 2 && choice % 10 <= 4) return 2
  return choicesLength < 4 ? 2 : 3
}

// The UI language is fixed to Russian by default so tests stay deterministic;
// `en` is complete and can be selected by setting `i18n.global.locale`.
export const i18n = createI18n({
  legacy: false,
  locale: 'ru',
  fallbackLocale: 'en',
  messages: { ru, en },
  pluralRules: { ru: russianPlural },
})
