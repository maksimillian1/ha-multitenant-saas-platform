import { queryOptions } from '@tanstack/react-query'
import { ApiError, api } from './client'

export const meQuery = queryOptions({
  queryKey: ['me'],
  queryFn: async ({ signal }) => {
    const { data, error, response } = await api.GET('/api/me', { signal })
    if (error) {
      throw new ApiError(response.status, error.error)
    }
    return data
  },
})
