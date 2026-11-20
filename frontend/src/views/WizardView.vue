<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Asset } from '../api/types'
import { MIN_PASSWORD_LENGTH, MNEMONIC_WORD_COUNTS, mnemonicWordCount } from '../lib/validation'
import { useImportCheckStore } from '../stores/importCheck'
import { useUiStore } from '../stores/ui'
import { useWalletsStore } from '../stores/wallets'

/** Addresses shown after an import so the user can compare them with the source wallet. */
const IMPORT_CHECK_ASSETS: readonly Asset[] = ['BTC', 'ETH', 'USDT']

const { t } = useI18n()
const router = useRouter()
const ui = useUiStore()
const wallets = useWalletsStore()
const importCheck = useImportCheckStore()

const step = ref(1)
const mode = ref<'create' | 'import'>('create')
const name = ref(t('wizard.defaultName'))
const wordCount = ref<12 | 24>(12)
const seed = ref('')
const mnemonic = ref('')
const seedVisible = ref(false)
const challengePositions = ref<number[]>([])
const challengeAnswers = ref<string[]>(['', '', ''])
const password = ref('')
const passphrase = ref('')
const passphraseConfirm = ref('')
const error = ref('')
const busy = ref(false)

function clearSecrets(): void {
  seed.value = ''
  mnemonic.value = ''
  seedVisible.value = false
  challengePositions.value = []
  challengeAnswers.value = ['', '', '']
  password.value = ''
  passphrase.value = ''
  passphraseConfirm.value = ''
}

function stepLabel(): string {
  if (step.value <= 2) return t('wizard.stepOf', { step: step.value })
  if (step.value === 3) return t('wizard.importProtection')
  return step.value === 4 ? t('wizard.seedConfirmation') : t('wizard.creating')
}

function nextFromSeed(): void {
  error.value = ''
  if (mode.value === 'create') {
    void prepareWallet()
    return
  }
  step.value = 3
}

async function prepareWallet(): Promise<void> {
  if (!name.value.trim()) {
    error.value = t('wizard.nameRequired')
    return
  }
  busy.value = true
  try {
    const result = await api.prepareWallet(name.value.trim(), wordCount.value)
    mnemonic.value = result.mnemonic
    challengePositions.value = result.challengePositions
    challengeAnswers.value = ['', '', '']
    seedVisible.value = false
    step.value = 4
  } catch {
    error.value = t('wizard.prepareFailed')
  } finally {
    busy.value = false
  }
}

async function verifyMnemonic(): Promise<void> {
  error.value = ''
  if (password.value.length < MIN_PASSWORD_LENGTH) {
    error.value = t('validation.passwordLength', { min: MIN_PASSWORD_LENGTH })
    return
  }
  if (passphrase.value !== passphraseConfirm.value) {
    error.value = t('wizard.passphraseMismatch')
    return
  }
  busy.value = true
  try {
    const wallet = await api.confirmCreateWallet(
      name.value.trim(),
      password.value,
      passphrase.value,
      challengePositions.value,
      challengeAnswers.value,
    )
    wallets.add(wallet)
    await wallets.refreshWallet(wallet.id, false)
    await router.push({ name: 'home' })
  } catch {
    error.value = t('wizard.verifyFailed')
  } finally {
    busy.value = false
    password.value = ''
    passphrase.value = ''
    passphraseConfirm.value = ''
  }
}

async function importWallet(): Promise<void> {
  error.value = ''
  if (!name.value.trim() || password.value.length < MIN_PASSWORD_LENGTH) {
    error.value = t('wizard.importFieldsRequired', { min: MIN_PASSWORD_LENGTH })
    return
  }
  if (!MNEMONIC_WORD_COUNTS.includes(mnemonicWordCount(seed.value))) {
    error.value = t('wizard.seedWordCount')
    return
  }
  busy.value = true
  try {
    const wallet = await api.importWallet(name.value.trim(), seed.value.trim(), password.value, passphrase.value)
    wallets.add(wallet)
    await wallets.refreshWallet(wallet.id, false)
    const results = await Promise.allSettled(IMPORT_CHECK_ASSETS.map(asset => api.receive(wallet.id, asset)))
    importCheck.addresses = results.flatMap((result, index) => {
      const asset = IMPORT_CHECK_ASSETS[index]
      return result.status === 'fulfilled' && asset ? [{ asset, address: result.value }] : []
    })
    const incomplete = results.some(result => result.status === 'rejected')
    await router.push({ name: 'import-check' })
    // The router guard clears feedback on navigation, so set it afterwards.
    if (incomplete) ui.notice = t('wizard.addressesIncomplete')
  } catch {
    error.value = t('wizard.importFailed')
  } finally {
    busy.value = false
    password.value = ''
    passphrase.value = ''
    seed.value = ''
  }
}

function toVerification(): void {
  error.value = ''
  step.value = 5
}

function showPhraseAgain(): void {
  step.value = 4
  error.value = ''
  challengeAnswers.value = ['', '', '']
}

onBeforeUnmount(() => {
  void api.cancelCreateWallet().catch(() => undefined)
  clearSecrets()
})
</script>

<template>
  <section class="stack narrow">
    <div class="steps">{{ stepLabel() }}</div>
    <article class="card stack">
      <template v-if="step === 1">
        <h2>{{ t('wizard.howToAdd') }}</h2>
        <div class="row">
          <button type="button" :class="{ selected: mode === 'create' }" @click="mode = 'create'">{{ t('wizard.createNew') }}</button>
          <button type="button" :class="{ selected: mode === 'import' }" @click="mode = 'import'">{{ t('wizard.import') }}</button>
        </div>
        <label for="wizard-name">{{ t('wizard.name') }}</label>
        <input id="wizard-name" v-model="name" autocomplete="off">
        <button class="primary" type="button" @click="step = 2">{{ t('common.continue') }}</button>
      </template>

      <template v-else-if="step === 2">
        <h2>{{ mode === 'create' ? t('wizard.seedParams') : t('wizard.seedImport') }}</h2>
        <p class="warning">{{ t('wizard.seedWarning') }}</p>
        <template v-if="mode === 'create'">
          <label for="wizard-words">{{ t('wizard.wordCount') }}</label>
          <select id="wizard-words" v-model.number="wordCount">
            <option :value="12">{{ t('wizard.words12') }}</option>
            <option :value="24">{{ t('wizard.words24') }}</option>
          </select>
          <p class="muted">{{ t('wizard.generatedByGo') }}</p>
        </template>
        <textarea
          v-else
          v-model="seed"
          rows="4"
          autocomplete="off"
          spellcheck="false"
          :placeholder="t('wizard.seedPlaceholder')"
        ></textarea>
        <button class="primary" type="button" :disabled="busy" @click="nextFromSeed">
          {{ mode === 'create' ? t('wizard.generate') : t('common.continue') }}
        </button>
        <p v-if="error" class="error" role="alert">{{ error }}</p>
      </template>

      <template v-else-if="step === 3">
        <h2>{{ t('wizard.protectImport') }}</h2>
        <label for="wizard-import-password">{{ t('wizard.passwordMin', { min: MIN_PASSWORD_LENGTH }) }}</label>
        <input id="wizard-import-password" v-model="password" type="password" autocomplete="off">
        <label for="wizard-import-passphrase">{{ t('wizard.passphraseOptional') }}</label>
        <input id="wizard-import-passphrase" v-model="passphrase" type="password" autocomplete="off">
        <p v-if="error" class="error" role="alert">{{ error }}</p>
        <button class="primary" type="button" :disabled="busy" @click="importWallet">{{ t('wizard.importWallet') }}</button>
      </template>

      <template v-else-if="step === 4">
        <h2>{{ t('wizard.writeDown') }}</h2>
        <p class="warning">{{ t('wizard.writeDownWarning') }}</p>
        <p v-if="seedVisible" class="seed">{{ mnemonic }}</p>
        <button class="secondary" type="button" @click="seedVisible = !seedVisible">
          {{ seedVisible ? t('wizard.hidePhrase') : t('wizard.showPhrase') }}
        </button>
        <p class="muted">{{ t('wizard.phraseHidden') }}</p>
        <button
          class="primary"
          type="button"
          :disabled="!seedVisible"
          @click="toVerification"
        >
          {{ t('wizard.toVerification') }}
        </button>
      </template>

      <template v-else>
        <h2>{{ t('wizard.verifyTitle') }}</h2>
        <p class="muted">{{ t('wizard.verifyHint') }}</p>
        <label v-for="(position, index) in challengePositions" :key="position">
          {{ t('wizard.wordNumber', { n: position + 1 }) }}
          <input
            v-model="challengeAnswers[index]"
            autocomplete="off"
            autocapitalize="none"
            spellcheck="false">
        </label>
        <label for="wizard-password">{{ t('wizard.passwordMin', { min: MIN_PASSWORD_LENGTH }) }}</label>
        <input id="wizard-password" v-model="password" type="password" autocomplete="new-password">
        <label for="wizard-passphrase">{{ t('wizard.passphraseOptional') }}</label>
        <input id="wizard-passphrase" v-model="passphrase" type="password" autocomplete="off">
        <label for="wizard-passphrase-confirm">{{ t('wizard.passphraseRepeat') }}</label>
        <input id="wizard-passphrase-confirm" v-model="passphraseConfirm" type="password" autocomplete="off">
        <p v-if="error" class="error" role="alert">{{ error }}</p>
        <button class="primary" type="button" :disabled="busy" @click="verifyMnemonic">{{ t('wizard.verifyAndCreate') }}</button>
        <button class="quiet" type="button" @click="showPhraseAgain">{{ t('wizard.showAgain') }}</button>
      </template>

      <button v-if="step < 4" class="quiet" type="button" @click="step = Math.max(1, step - 1)">← {{ t('common.back') }}</button>
    </article>
  </section>
</template>
