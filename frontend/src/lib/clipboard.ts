import { i18n } from '../i18n'
import { useUiStore } from '../stores/ui'

/** Copies text and reports the outcome through the shared notice area. */
export function useClipboard() {
  const ui = useUiStore()

  async function copyText(text: string): Promise<void> {
    const t = i18n.global.t
    try {
      const clipboard = globalThis.navigator.clipboard
      if (!clipboard?.writeText) throw new Error('clipboard unavailable')
      await clipboard.writeText(text)
      ui.notice = t('clipboard.copied')
    } catch {
      ui.notice = t('clipboard.failed')
    }
  }

  return { copyText }
}
