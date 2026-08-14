<template>
  <div class="rounded-xl bg-gray-50 p-3 dark:bg-dark-800/70">
    <div class="flex items-center justify-between text-sm">
      <span class="font-medium text-gray-700 dark:text-gray-300">{{ label }}</span>
      <span class="font-semibold text-gray-900 dark:text-white">{{ usedLabel }}</span>
    </div>
    <div class="mt-2 h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
      <div class="h-full rounded-full transition-all" :class="barClass" :style="{ width: `${normalizedUsed}%` }"></div>
    </div>
    <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ t('readonly.fields.resetsAt') }}: {{ formattedReset }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ label: string; used?: number | null; resetAt?: string | null }>()
const { t } = useI18n()
const normalizedUsed = computed(() => Math.max(0, Math.min(100, Number(props.used ?? 0))))
const usedLabel = computed(() => props.used == null ? '-' : `${normalizedUsed.value.toFixed(1)}%`)
const barClass = computed(() => normalizedUsed.value >= 90 ? 'bg-red-500' : normalizedUsed.value >= 70 ? 'bg-amber-500' : 'bg-emerald-500')
const formattedReset = computed(() => {
  if (!props.resetAt) return '-'
  const date = new Date(props.resetAt)
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString()
})
</script>
