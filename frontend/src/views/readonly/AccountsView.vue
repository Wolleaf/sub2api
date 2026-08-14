<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="rounded-2xl border border-blue-200 bg-blue-50 px-5 py-4 text-sm text-blue-800 dark:border-blue-900/60 dark:bg-blue-950/30 dark:text-blue-200">
        {{ t('readonly.notice') }}
      </div>

      <form class="flex flex-wrap gap-3 rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900" @submit.prevent="applyFilters">
        <input v-model="draft.search" class="input min-w-56 flex-1" :placeholder="t('readonly.accounts.search')" />
        <select v-model="draft.platform" class="input w-full sm:w-40">
          <option value="">{{ t('readonly.filters.allPlatforms') }}</option>
          <option value="openai">OpenAI</option>
          <option value="anthropic">Anthropic</option>
          <option value="gemini">Gemini</option>
          <option value="antigravity">Antigravity</option>
          <option value="grok">Grok</option>
        </select>
        <select v-model="draft.status" class="input w-full sm:w-36">
          <option value="">{{ t('readonly.filters.allStatuses') }}</option>
          <option value="active">{{ t('common.active') }}</option>
          <option value="disabled">{{ t('readonly.status.disabled') }}</option>
          <option value="error">{{ t('readonly.status.error') }}</option>
        </select>
        <button type="submit" class="btn btn-primary" :disabled="loading">{{ t('common.search') }}</button>
      </form>

      <div v-if="loading" class="grid gap-4 xl:grid-cols-2">
        <div v-for="index in 4" :key="index" class="h-72 animate-pulse rounded-2xl bg-gray-200 dark:bg-dark-800"></div>
      </div>

      <div v-else-if="items.length" class="grid gap-4 xl:grid-cols-2">
        <article v-for="account in items" :key="account.id" class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-900">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0">
              <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ account.display_name }}</h2>
              <p v-if="account.masked_email" class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ account.masked_email }}</p>
            </div>
            <span :class="statusBadge(account.status)">{{ statusLabel(account.status) }}</span>
          </div>

          <dl class="mt-4 grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('readonly.fields.platform') }}</dt><dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ account.platform }}</dd></div>
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('readonly.fields.accountType') }}</dt><dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ account.type }}</dd></div>
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('readonly.fields.planType') }}</dt><dd class="mt-1 font-medium text-gray-900 dark:text-white">{{ account.plan_type || '-' }}</dd></div>
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('readonly.fields.schedulable') }}</dt><dd class="mt-1 font-medium" :class="account.schedulable ? 'text-emerald-600' : 'text-amber-600'">{{ account.schedulable ? t('common.yes') : t('common.no') }}</dd></div>
          </dl>

          <div class="mt-5 grid gap-3 sm:grid-cols-2">
            <UsageWindow :label="t('readonly.accounts.window5h')" :used="account.codex_5h_used_percent" :reset-at="account.codex_5h_reset_at" />
            <UsageWindow :label="t('readonly.accounts.window7d')" :used="account.codex_7d_used_percent" :reset-at="account.codex_7d_reset_at" />
          </div>

          <div class="mt-4 flex flex-wrap gap-2">
            <span v-for="group in account.groups" :key="group.id" class="rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-700 dark:bg-dark-800 dark:text-gray-300">
              {{ group.name }}
            </span>
          </div>

          <div class="mt-4 grid gap-1 border-t border-gray-100 pt-4 text-xs text-gray-500 dark:border-dark-800 dark:text-dark-400 sm:grid-cols-2">
            <span>{{ t('readonly.fields.lastUsed') }}: {{ formatTime(account.last_used_at) }}</span>
            <span>{{ t('readonly.fields.observedAt') }}: {{ formatTime(account.codex_usage_updated_at) }}</span>
          </div>
        </article>
      </div>

      <div v-else class="rounded-2xl border border-dashed border-gray-300 bg-white py-16 text-center text-gray-500 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-400">
        {{ t('readonly.accounts.empty') }}
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
import UsageWindow from './UsageWindow.vue'
import { readonlyAPI, type ReadonlyAccount, type ReadonlyListFilters } from '@/api/readonly'

const { t } = useI18n()
const items = ref<ReadonlyAccount[]>([])
const loading = ref(false)
const draft = reactive<ReadonlyListFilters>({ search: '', platform: '', status: '' })
const filters = reactive<ReadonlyListFilters>({})
const pagination = reactive({ page: 1, page_size: 20, total: 0 })

async function load() {
  loading.value = true
  try {
    const result = await readonlyAPI.listAccounts(pagination.page, pagination.page_size, filters)
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

function formatTime(value?: string | null): string {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString()
}

function statusLabel(status: string): string {
  const key = `readonly.status.${status}`
  const translated = t(key)
  return translated === key ? status : translated
}

function statusBadge(status: string): string {
  const base = 'rounded-full px-2.5 py-1 text-xs font-medium'
  if (status === 'active') return `${base} bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300`
  if (status === 'error') return `${base} bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300`
  return `${base} bg-gray-100 text-gray-700 dark:bg-dark-800 dark:text-gray-300`
}

onMounted(load)
</script>
