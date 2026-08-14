export default {
  readonly: {
    notice: '当前账号为分组范围只读管理员。页面仅展示已授权分组及脱敏后的账号运行状态。',
    nav: { accounts: '账号信息', groups: '分组信息' },
    filters: { allPlatforms: '全部平台', allStatuses: '全部状态' },
    status: { active: '正常', disabled: '已禁用', error: '异常' },
    fields: {
      platform: '平台',
      accountType: '账号类型',
      planType: '套餐类型',
      schedulable: '可调度',
      lastUsed: '最后使用',
      observedAt: '观测时间',
      resetsAt: '重置时间'
    },
    accounts: {
      title: '账号信息',
      description: '查看授权分组内的脱敏账号状态与上游额度窗口',
      search: '搜索账号显示名或平台',
      window5h: '5 小时窗口',
      window7d: '7 天窗口',
      empty: '当前授权范围内没有账号'
    },
    groups: {
      title: '分组信息',
      description: '查看已授权分组与账号状态汇总',
      search: '搜索分组名称',
      exclusive: '专属分组',
      standard: '标准分组',
      subscription: '订阅分组',
      totalAccounts: '账号总数',
      activeAccounts: '正常',
      limitedAccounts: '受限',
      empty: '尚未授权任何分组'
    }
  }
}
