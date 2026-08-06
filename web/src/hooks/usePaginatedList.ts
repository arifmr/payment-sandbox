import { useCallback, useMemo, useState } from 'react'
import { useAsync } from './useAsync'
import type { Paginated } from '@/api/types'

/**
 * Paged list state. Pairs with the backend's `{data, pagination:{page,page_size,total}}`
 * envelope so every list page shares one implementation of paging, filtering and the
 * loading/error/empty states from SRS §4.1.
 */

export const DEFAULT_PAGE_SIZE = 20
/** The backend caps page_size at 100; asking for more is silently clamped there. */
export const MAX_PAGE_SIZE = 100

export interface UsePaginatedListResult<T> {
  items: T[]
  page: number
  pageSize: number
  total: number
  totalPages: number
  isLoading: boolean
  isError: boolean
  error: Error | null
  /** True on a successful response with nothing in it — the empty state. */
  isEmpty: boolean
  setPage: (page: number) => void
  reload: () => void
}

/**
 * @param fetcher receives the 1-based page and returns one page of results.
 * @param deps    filter values. Changing any of them resets to page 1, because staying on
 *                page 7 of a freshly narrowed result set usually lands past the end and
 *                shows a confusing empty list.
 */
export function usePaginatedList<T>(
  fetcher: (params: { page: number; page_size: number }, signal: AbortSignal) => Promise<Paginated<T>>,
  deps: readonly unknown[] = [],
  pageSize: number = DEFAULT_PAGE_SIZE,
): UsePaginatedListResult<T> {
  const [page, setPageRaw] = useState(1)

  // A filter change makes the current page number meaningless.
  const depsKey = JSON.stringify(deps)
  const [lastDepsKey, setLastDepsKey] = useState(depsKey)
  if (depsKey !== lastDepsKey) {
    setLastDepsKey(depsKey)
    setPageRaw(1)
  }

  const state = useAsync(
    (signal) => fetcher({ page, page_size: pageSize }, signal),
    [page, pageSize, depsKey],
  )

  const setPage = useCallback((next: number) => {
    setPageRaw(Math.max(1, next))
  }, [])

  const { items, total, totalPages } = useMemo(() => {
    const data = state.data
    if (!data) return { items: [] as T[], total: 0, totalPages: 0 }
    return {
      items: data.data ?? [],
      total: data.pagination?.total ?? 0,
      totalPages: Math.max(1, Math.ceil((data.pagination?.total ?? 0) / pageSize)),
    }
  }, [state.data, pageSize])

  return {
    items,
    page,
    pageSize,
    total,
    totalPages,
    isLoading: state.isLoading,
    isError: state.isError,
    error: state.error,
    // Only meaningful once a response has arrived: during loading the list is empty too,
    // and showing "no data" mid-request flickers the wrong message.
    isEmpty: state.isSuccess && items.length === 0,
    setPage,
    reload: state.reload,
  }
}
