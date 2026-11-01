export interface TenantQueryResult<TEvent, TAlert> {
  events: TEvent[]
  alerts: TAlert[]
}

export interface TenantQueryOptions<TEvent, TAlert> {
  apiBase: string
  tenantId: string
  generation: number
  signal: AbortSignal
  isCurrentGeneration: (gen: number) => boolean
  onSuccess: (result: TenantQueryResult<TEvent, TAlert>) => void
  onError: (error: Error) => void
  fetchFn?: typeof fetch
}

/**
 * Shared production request logic for fetching events and alerts with generation checking
 * and signal cancellation, ensuring delayed responses cannot overwrite current-tenant state.
 */
export async function fetchTenantData<TEvent, TAlert>(
  options: TenantQueryOptions<TEvent, TAlert>
): Promise<boolean> {
  const { apiBase, tenantId, generation, signal, isCurrentGeneration, onSuccess, onError } = options
  const fetchImpl = options.fetchFn ?? fetch
  const trimmedTenant = tenantId.trim()
  if (!trimmedTenant) {
    return false
  }

  const tenantParam = encodeURIComponent(trimmedTenant)
  const normalizedBase = apiBase.trim().replace(/\/+$/, '')

  const getJSON = async <T,>(path: string): Promise<T[]> => {
    const response = await fetchImpl(`${normalizedBase}${path}`, { signal })
    if (!response.ok) {
      const message = await response.text()
      throw new Error(`API error: HTTP ${response.status} (${message.trim() || response.statusText})`)
    }
    const data: unknown = await response.json()
    if (!Array.isArray(data)) {
      throw new Error('API error: expected an array response')
    }
    return data as T[]
  }

  try {
    const [nextEvents, nextAlerts] = await Promise.all([
      getJSON<TEvent>(`/v1/events?tenant_id=${tenantParam}`),
      getJSON<TAlert>(`/v1/alerts?tenant_id=${tenantParam}`),
    ])

    if (!signal.aborted && isCurrentGeneration(generation)) {
      onSuccess({ events: nextEvents, alerts: nextAlerts })
      return true
    }
  } catch (error: unknown) {
    if (!signal.aborted && isCurrentGeneration(generation)) {
      const err = error instanceof Error ? error : new Error('Failed to connect to backend API')
      onError(err)
    }
  }
  return false
}
