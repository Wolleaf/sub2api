import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import UpstreamWeeklyQuotaCard from '../UpstreamWeeklyQuotaCard.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('UpstreamWeeklyQuotaCard', () => {
  it('shows percentage shares independently of dollar billing', () => {
    const wrapper = mount(UpstreamWeeklyQuotaCard, { props: { quota: {
      upstream_weekly_limit_percent: 25, upstream_weekly_usage_percent: 5.82,
      upstream_weekly_observed_at: '2026-09-07T12:00:00Z', upstream_weekly_window_start: '2026-09-07T10:31:48Z'
    }, details: true } })
    expect(wrapper.text()).toContain('5.82% / 25%')
    expect(wrapper.text()).toContain('keys.upstreamWeeklyQuotaNote')
    expect(wrapper.text()).not.toContain('$')
  })
  it('does not present an uninitialized share as a measured zero', () => {
    const wrapper = mount(UpstreamWeeklyQuotaCard, { props: { quota: { upstream_weekly_limit_percent: 25 } } })
    expect(wrapper.text()).toContain('— / 25%')
    expect(wrapper.text()).toContain('keys.upstreamWeeklySyncing')
  })
})
