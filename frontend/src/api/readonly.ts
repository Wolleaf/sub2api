import { apiClient } from './client'
import type { PaginatedResponse } from '@/types'

export interface ReadonlyGroup {
  id: number
  name: string
  platform: string
  status: string
  subscription_type: string
  is_exclusive: boolean
  account_count: number
  active_account_count: number
  rate_limited_account_count: number
}

export interface ReadonlyAccountGroup {
  id: number
  name: string
  platform: string
  status: string
}

export interface ReadonlyAccount {
  id: number
  display_name: string
  platform: string
  type: string
  plan_type?: string
  masked_email?: string
  status: string
  schedulable: boolean
  last_used_at?: string | null
  rate_limited: boolean
  rate_limit_reset_at?: string | null
  codex_5h_used_percent?: number | null
  codex_5h_reset_at?: string | null
  codex_7d_used_percent?: number | null
  codex_7d_reset_at?: string | null
  codex_usage_updated_at?: string | null
  groups: ReadonlyAccountGroup[]
}

export interface ReadonlyListFilters {
  search?: string
  platform?: string
  status?: string
  type?: string
}

async function listGroups(
  page = 1,
  pageSize = 20,
  filters: ReadonlyListFilters = {}
): Promise<PaginatedResponse<ReadonlyGroup>> {
  const { data } = await apiClient.get<PaginatedResponse<ReadonlyGroup>>('/admin/readonly/groups', {
    params: { page, page_size: pageSize, ...filters }
  })
  return data
}

async function getGroup(id: number): Promise<ReadonlyGroup> {
  const { data } = await apiClient.get<ReadonlyGroup>(`/admin/readonly/groups/${id}`)
  return data
}

async function listAccounts(
  page = 1,
  pageSize = 20,
  filters: ReadonlyListFilters = {}
): Promise<PaginatedResponse<ReadonlyAccount>> {
  const { data } = await apiClient.get<PaginatedResponse<ReadonlyAccount>>('/admin/readonly/accounts', {
    params: { page, page_size: pageSize, ...filters }
  })
  return data
}

async function getAccount(id: number): Promise<ReadonlyAccount> {
  const { data } = await apiClient.get<ReadonlyAccount>(`/admin/readonly/accounts/${id}`)
  return data
}

export const readonlyAPI = {
  listGroups,
  getGroup,
  listAccounts,
  getAccount
}

export default readonlyAPI
