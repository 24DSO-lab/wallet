<script setup lang="ts">
import AssetCatalogCard from '../components/settings/AssetCatalogCard.vue'
import AutoLockCard from '../components/settings/AutoLockCard.vue'
import BackupCard from '../components/settings/BackupCard.vue'
import ChangePasswordCard from '../components/settings/ChangePasswordCard.vue'
import DeleteWalletCard from '../components/settings/DeleteWalletCard.vue'
import EtherscanKeyCard from '../components/settings/EtherscanKeyCard.vue'
import NetworkProfileCard from '../components/settings/NetworkProfileCard.vue'
import RenameWalletCard from '../components/settings/RenameWalletCard.vue'
import RevealSeedCard from '../components/settings/RevealSeedCard.vue'
import SelectedWalletCard from '../components/settings/SelectedWalletCard.vue'
</script>

<template>
  <section class="stack narrow">
    <NetworkProfileCard />
    <AssetCatalogCard />
    <EtherscanKeyCard />
    <AutoLockCard />
    <BackupCard />
    <ChangePasswordCard />
    <SelectedWalletCard />
    <RenameWalletCard />
    <RevealSeedCard />
    <DeleteWalletCard />
  </section>
</template>
