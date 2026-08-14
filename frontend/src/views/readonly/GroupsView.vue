<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="rounded-2xl border border-blue-200 bg-blue-50 px-5 py-4 text-sm text-blue-800 dark:border-blue-900/60 dark:bg-blue-950/30 dark:text-blue-200">
        {{ t('readonly.notice') }}
      </div>

      <form class="flex flex-wrap gap-3 rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900" @submit.prevent="applyFilters">
        <input v-model="draft.search" class="input min-w-56 flex-1" :placeholder="t('readonly.groups.search')" />
        <select v-model="draft.platform" class="input w-full sm:w-40">
          <option value="">{{ t('readonly.filters.allPlatforms') }}</option>
          <option value="openai">OpenAI</option>
          <option value="anthropic">Anthropic</option>
          <option value="gemini">Gemini</option>
          <option value="antigravity">Antigravity</option>
          <option value="grok">Grok</option>
        </select>
        <button type="submit" class="btn btn-primary" :disabled="loading">{{ t('common.search') }}</button>
      </form>

      <div v-if="loading" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <div v-for="index in 6" :key="index" class="h-52 animate-pulse rounded-2xl bg-gray-200 dark:bg-dark-800"></div>
      </div>

      <div v-else-if="items.length" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <article v-for="group in items" :key="group.id" class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ group.name }}</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ group.platform }}</p>
            </div>
            <span
              :class="
                group.status === 'active'
                  ? 'rounded-full bg-emerald-100 px-2.5 py-1 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
                  : 'badge badge-gray'
              "
            >
              {{ statusLabel(group.status) }}
            </span>
          </div>

          <div class="mt-4 flex flex-wrap gap-2 text-xs">
            <span class="rounded-full bg-gray-100 px-2.5 py-1 font-medium text-gray-700 dark:bg-dark-800 dark:text-gray-300">{{ subscriptionLabel(group.subscription_type) }}</span>
            <span v-if="group.is_exclusive" class="rounded-full bg-purple-100 px-2.5 py-1 font-medium text-purple-700 dark:bg-purple-900/30 dark:text-purple-300">{{ t('readonly.groups.exclusive') }}</span>
          </div>

          <dl class="mt-5 grid grid-cols-3 gap-2 text-center">
            <div class="rounded-xl bg-gray-50 p-3 dark:bg-dark-800/70"><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('readonly.groups.totalAccounts') }}</dt><dd class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">{{ group.account_count }}</dd></div>
            <div class="rounded-xl bg-emerald-50 p-3 dark:bg-emerald-950/30"><dt class="text-xs text-emerald-700 dark:text-emerald-300">{{ t('readonly.groups.activeAccounts') }}</dt><dd class="mt-1 text-xl font-semibold text-emerald-700 dark:text-emerald-300">{{ group.active_account_count }}</dd></div>
            <div class="rounded-xl bg-amber-50 p-3 dark:bg-amber-950/30"><dt class="text-xs text-amber-700 dark:text-amber-300">{{ t('readonly.groups.limitedAccounts') }}</dt><dd class="mt-1 text-xl font-semibold text-amber-700 dark:text-amber-300">{{ group.rate_limited_account_count }}</dd></div>
          </dl>
        </article>
      </div>

      <div v-else class="rounded-2xl border border-dashed border-gray-300 bg-white py-16 text-center text-gray-500 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-400">
        {{ t('readonly.groups.empty') }}
      </div>

      <Pagination v-if="pagination.total > 0" :page="pagination.page" :total="pagination.total" :page-size="pagination.page_size" :show-page-size-selector="false" @update:page="changePage" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import { readonlyAPI, type ReadonlyGroup, type ReadonlyListFilters } from '@/api/readonly'

const { t } = useI18n()
const items = ref<ReadonlyGroup[]>([])
const loading = ref(false)
const draft = reactive<ReadonlyListFilters>({ search: '', platform: '' })
const filters = reactive<ReadonlyListFilters>({})
const pagination = reactive({ page: 1, page_size: 20, total: 0 })

async function load() {
  loading.value = true
  try {
    const result = await readonlyAPI.listGroups(pagination.page, pagination.page_size, filters)
    items.value = result.items
    pagination.total = result.total
  } finally {
    loading.value = false
  }
}

function applyFilters() {
  Object.assign(filters, draft)
  pagination.page = 1
  void load()
}

function changePage(page: number) {
  pagination.page = page
  void load()
}

function statusLabel(status: string): string {
  const key = `readonly.status.${status}`
  const translated = t(key)
  return translated === key ? status : translated
}

function subscriptionLabel(type: string): string {
  return type === 'subscription' ? t('readonly.groups.subscription') : t('readonly.groups.standard')
}

onMounted(load)
</script>
