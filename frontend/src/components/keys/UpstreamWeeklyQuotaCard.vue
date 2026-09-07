<template>
  <div class="space-y-1.5 min-w-[170px]" :title="t('keys.upstreamWeeklyQuotaNote')">
    <div class="text-xs font-medium text-gray-700 dark:text-gray-200">{{ t('keys.upstreamWeeklyQuota') }}</div>
    <div class="flex items-center justify-between gap-3 text-xs tabular-nums">
      <span class="text-gray-500">{{ t('keys.upstreamWeeklyEstimated') }}</span>
      <span :class="used >= limit ? 'text-red-500' : 'text-emerald-600 dark:text-emerald-400'">
        {{ quota.upstream_weekly_observed_at ? used.toFixed(2) + '%' : '—' }} / {{ limit }}%
      </span>
    </div>
    <div class="h-1.5 overflow-hidden rounded bg-gray-200 dark:bg-dark-600">
      <div class="h-full rounded" :class="used >= limit ? 'bg-red-500' : 'bg-emerald-500'" :style="{ width: progress + '%' }" />
    </div>
    <div v-if="resetAt" class="text-[10px] text-gray-500">{{ t('keys.upstreamWeeklyReset') }}: {{ resetAt }}</div>
    <div v-if="!quota.upstream_weekly_observed_at" class="text-xs text-amber-600">{{ t('keys.upstreamWeeklySyncing') }}</div>
    <p v-if="details" class="text-xs text-gray-500">{{ t('keys.upstreamWeeklyQuotaNote') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ApiKey } from '@/types'

const props = defineProps<{ quota: Pick<ApiKey, 'upstream_weekly_limit_percent' | 'upstream_weekly_usage_percent' | 'upstream_weekly_window_start' | 'upstream_weekly_observed_at'>; details?: boolean }>()
const { t } = useI18n()
const used = computed(() => props.quota.upstream_weekly_usage_percent ?? 0)
const limit = computed(() => props.quota.upstream_weekly_limit_percent ?? 0)
const progress = computed(() => limit.value > 0 ? Math.min(100, Math.max(0, used.value / limit.value * 100)) : 0)
const resetAt = computed(() => {
  if (!props.quota.upstream_weekly_window_start) return ''
  const start = new Date(props.quota.upstream_weekly_window_start).getTime()
  return Number.isFinite(start) ? new Date(start + 7 * 86400000).toLocaleString() : ''
})
</script>
