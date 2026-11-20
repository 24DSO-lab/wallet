import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const BCP47: Record<string, string> = { ru: 'ru-RU', en: 'en-US' }

/** Date/time formatters bound to the active UI language. */
export function useDateFormat() {
  const { locale } = useI18n()
  const tag = computed(() => BCP47[locale.value] ?? 'en-US')

  return {
    time: (iso: string): string => new Date(iso).toLocaleTimeString(tag.value),
    dateTime: (iso: string): string => new Date(iso).toLocaleString(tag.value),
  }
}
