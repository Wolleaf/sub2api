export default {
  readonly: {
    notice: 'This is a group-scoped readonly administrator account. Only authorized groups and redacted account health are shown.',
    nav: { accounts: 'Accounts', groups: 'Groups' },
    filters: { allPlatforms: 'All platforms', allStatuses: 'All statuses' },
    status: { active: 'Active', disabled: 'Disabled', error: 'Error' },
    fields: {
      platform: 'Platform',
      accountType: 'Account type',
      planType: 'Plan',
      schedulable: 'Schedulable',
      lastUsed: 'Last used',
      observedAt: 'Observed at',
      resetsAt: 'Resets at'
    },
    accounts: {
      title: 'Account information',
      description: 'View redacted account health and upstream quota windows in authorized groups',
      search: 'Search display name or platform',
      window5h: '5-hour window',
      window7d: '7-day window',
      empty: 'No accounts are available in the authorized scope'
    },
    groups: {
      title: 'Group information',
      description: 'View authorized groups and account health summaries',
      search: 'Search group name',
      exclusive: 'Exclusive',
      standard: 'Standard',
      subscription: 'Subscription',
      totalAccounts: 'Total',
      activeAccounts: 'Active',
      limitedAccounts: 'Limited',
      empty: 'No groups have been authorized'
    }
  }
}
