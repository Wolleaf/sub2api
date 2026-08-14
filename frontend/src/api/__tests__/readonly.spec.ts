import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({
  get: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get,
  },
}))

import { readonlyAPI } from '@/api/readonly'

describe('scoped readonly API', () => {
  beforeEach(() => {
    get.mockReset()
  })

  it('uses only the dedicated group endpoints', async () => {
    const listResponse = { items: [], total: 0, page: 2, page_size: 10 }
    const detailResponse = { id: 7, name: 'OpenAI', platform: 'openai' }
    get.mockResolvedValueOnce({ data: listResponse }).mockResolvedValueOnce({ data: detailResponse })

    await expect(
      readonlyAPI.listGroups(2, 10, { search: 'Open', platform: 'openai', status: 'active' })
    ).resolves.toEqual(listResponse)
    expect(get).toHaveBeenNthCalledWith(1, '/admin/readonly/groups', {
      params: {
        page: 2,
        page_size: 10,
        search: 'Open',
        platform: 'openai',
        status: 'active',
      },
    })

    await expect(readonlyAPI.getGroup(7)).resolves.toEqual(detailResponse)
    expect(get).toHaveBeenNthCalledWith(2, '/admin/readonly/groups/7')
  })

  it('uses only the dedicated account endpoints', async () => {
    const listResponse = { items: [], total: 0, page: 1, page_size: 20 }
    const detailResponse = { id: 11, display_name: 'op***@example.com', platform: 'openai' }
    get.mockResolvedValueOnce({ data: listResponse }).mockResolvedValueOnce({ data: detailResponse })

    await expect(readonlyAPI.listAccounts()).resolves.toEqual(listResponse)
    expect(get).toHaveBeenNthCalledWith(1, '/admin/readonly/accounts', {
      params: { page: 1, page_size: 20 },
    })

    await expect(readonlyAPI.getAccount(11)).resolves.toEqual(detailResponse)
    expect(get).toHaveBeenNthCalledWith(2, '/admin/readonly/accounts/11')
  })
})
