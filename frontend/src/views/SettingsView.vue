<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AddWalletCard from '../components/settings/AddWalletCard.vue'
import AssetCatalogCard from '../components/settings/AssetCatalogCard.vue'
import AutoLockCard from '../components/settings/AutoLockCard.vue'
import BackupCard from '../components/settings/BackupCard.vue'
import ChangePasswordCard from '../components/settings/ChangePasswordCard.vue'
import DeleteWalletCard from '../components/settings/DeleteWalletCard.vue'
import EtherscanKeyCard from '../components/settings/EtherscanKeyCard.vue'
import LogCard from '../components/settings/LogCard.vue'
import NetworkProfileCard from '../components/settings/NetworkProfileCard.vue'
import RenameWalletCard from '../components/settings/RenameWalletCard.vue'
import RevealSeedCard from '../components/settings/RevealSeedCard.vue'
import { useWalletsStore } from '../stores/wallets'

const GENERAL = 'general'

const { t } = useI18n()
const wallets = useWalletsStore()
const active = ref(GENERAL)

const isGeneral = computed(() => active.value === GENERAL)

function open(tab: string): void {
  active.value = tab
  // The per-wallet cards act on the selected wallet, so a wallet tab selects it.
  if (tab !== GENERAL) wallets.select(tab)
}

// A deleted wallet takes its tab with it.
watch(
  () => wallets.wallets.map(wallet => wallet.id),
  ids => {
    if (active.value !== GENERAL && !ids.includes(active.value)) active.value = GENERAL
  },
)
</script>

<template>
  <section class="stack narrow">
    <div class="tabs" role="tablist" :aria-label="t('settings.tabs.label')">
      <button
        class="tab"
        type="button"
        role="tab"
        :aria-selected="isGeneral"
        :class="{ active: isGeneral }"
        @click="open(GENERAL)"
      >
        {{ t('settings.tabs.general') }}
      </button>
      <button
        v-for="wallet in wallets.wallets"
        :key="wallet.id"
        class="tab"
        type="button"
        role="tab"
        :aria-selected="active === wallet.id"
        :class="{ active: active === wallet.id }"
        @click="open(wallet.id)"
      >
        {{ wallet.name }}
      </button>
    </div>

    <template v-if="isGeneral">
      <AddWalletCard />
      <NetworkProfileCard />
      <EtherscanKeyCard />
      <AutoLockCard />
      <BackupCard />
      <ChangePasswordCard />
      <LogCard />
    </template>
    <template v-else>
      <AssetCatalogCard />
      <RenameWalletCard />
      <RevealSeedCard />
      <DeleteWalletCard />
    </template>
  </section>
</template>
