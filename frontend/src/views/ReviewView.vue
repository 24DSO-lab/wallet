<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useSendStore } from '../stores/send'

const { t } = useI18n()
const router = useRouter()
const send = useSendStore()
</script>

<template>
  <section v-if="send.quote" class="card narrow stack">
    <h2>{{ send.quote.replacesTxId ? t('review.rbfTitle') : t('review.title') }}</h2>
    <p v-if="send.quote.replacesTxId" class="warning">{{ t('review.rbfWarning', { txid: send.quote.replacesTxId }) }}</p>
    <dl>
      <dt>{{ t('review.network') }}</dt>
      <dd>{{ send.quote.network }}</dd>
      <dt>{{ t('review.asset') }}</dt>
      <dd>{{ send.quote.asset }}</dd>
      <dt>{{ t('review.recipient') }}</dt>
      <dd class="break">{{ send.quote.recipient }}</dd>
      <dt>{{ t('review.amount') }}</dt>
      <dd>{{ send.quote.amount }} {{ send.quote.asset }}</dd>
      <dt>{{ t('review.fee') }}</dt>
      <dd>{{ send.quote.fee }} {{ send.quote.feeAsset }}</dd>
      <template v-if="send.quote.feeAsset === send.quote.asset">
        <dt>{{ t('review.total') }}</dt>
        <dd>{{ send.quote.total }} {{ send.quote.asset }}</dd>
      </template>
    </dl>
    <p v-for="warning in send.quote.warnings" :key="warning" class="warning">{{ warning }}</p>
    <button class="primary" type="button" @click="router.push({ name: 'confirm' })">{{ t('common.next') }}</button>
  </section>
</template>
