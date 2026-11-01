import { useEffect, useState } from 'react'

interface Event {
  event_id: string
  tenant_id: string
  site_id: string
  occurred_at: string
  severity: string
  source: string
  category: string
}

interface Alert {
  alert_id: string
  event_id: string
  tenant_id: string
  severity: string
  description: string
  created_at: string
}

function App() {
  const [events, setEvents] = useState<Event[]>([])
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [tenantId, setTenantId] = useState<string>('test-tenant')
  const [errorMessage, setErrorMessage] = useState<string | null>(null)
  const [lastFetched, setLastFetched] = useState<string | null>(null)

  const validationError = !tenantId.trim() ? 'tenant_id must not be empty' : null
  const displayedError = validationError || errorMessage

  useEffect(() => {
    const trimmedTenant = tenantId.trim()
    if (!trimmedTenant) {
      return
    }

    // Never display the previous tenant's data while the new query loads.
    setEvents([])
    setAlerts([])
    setLastFetched(null)
    setErrorMessage(null)

    const abortController = new AbortController()
    let refreshTimer: ReturnType<typeof setTimeout> | undefined
    const tenantParam = encodeURIComponent(trimmedTenant)

    const getJSON = async <T,>(path: string): Promise<T[]> => {
      // Same-origin requests work with the Vite development proxy and with a
      // production reverse proxy serving the dashboard and /v1 API together.
      const response = await fetch(path, { signal: abortController.signal })
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

    const refresh = async () => {
      try {
        const [nextEvents, nextAlerts] = await Promise.all([
          getJSON<Event>(`/v1/events?tenant_id=${tenantParam}`),
          getJSON<Alert>(`/v1/alerts?tenant_id=${tenantParam}`),
        ])
        if (!abortController.signal.aborted) {
          setEvents(nextEvents)
          setAlerts(nextAlerts)
          setErrorMessage(null)
          setLastFetched(new Date().toLocaleTimeString())
        }
      } catch (error: unknown) {
        if (!abortController.signal.aborted) {
          console.error('Failed to fetch data:', error)
          setErrorMessage(error instanceof Error ? error.message : 'Failed to connect to backend API')
        }
      } finally {
        // Schedule only after a request finishes to avoid overlapping polls.
        if (!abortController.signal.aborted) {
          refreshTimer = setTimeout(refresh, 2000)
        }
      }
    }

    void refresh()
    return () => {
      abortController.abort()
      if (refreshTimer !== undefined) clearTimeout(refreshTimer)
    }
  }, [tenantId])

  return (
    <div style={{ padding: '20px', fontFamily: 'sans-serif' }}>
      <h1>RedCyberFox Dashboard (Phase 1 Vertical Slice)</h1>

      <div style={{ marginBottom: '15px', display: 'flex', alignItems: 'center', gap: '10px' }}>
        <label htmlFor="tenant-input"><strong>Tenant ID:</strong></label>
        <input
          id="tenant-input"
          type="text"
          value={tenantId}
          onChange={(e) => setTenantId(e.target.value)}
          style={{ padding: '5px 10px', fontSize: '14px', width: '200px' }}
        />
        {lastFetched && <span style={{ color: '#666', fontSize: '12px' }}>Last updated: {lastFetched}</span>}
      </div>

      {displayedError && (
        <div style={{ padding: '10px', marginBottom: '15px', backgroundColor: '#ffebee', color: '#c62828', border: '1px solid #ef9a9a', borderRadius: '4px' }}>
          <strong>Error:</strong> {displayedError}
        </div>
      )}

      <div style={{ display: 'flex', gap: '20px' }}>
        <div style={{ flex: 1, border: '1px solid #ccc', padding: '10px' }}>
          <h2>Recent Events (Count: {events.length})</h2>
          <table style={{ width: '100%', textAlign: 'left' }}>
            <thead>
              <tr>
                <th>Time</th>
                <th>Tenant</th>
                <th>Source</th>
                <th>Severity</th>
              </tr>
            </thead>
            <tbody>
              {events.map(ev => (
                <tr key={ev.event_id}>
                  <td>{new Date(ev.occurred_at).toLocaleTimeString()}</td>
                  <td>{ev.tenant_id}</td>
                  <td>{ev.source}</td>
                  <td style={{ color: ev.severity === 'CRITICAL' ? 'red' : 'black' }}>
                    {ev.severity}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div style={{ flex: 1, border: '1px solid red', padding: '10px', backgroundColor: '#fff0f0' }}>
          <h2 style={{ color: 'red' }}>Phase-1 Alerts (Count: {alerts.length})</h2>
          <table style={{ width: '100%', textAlign: 'left' }}>
            <thead>
              <tr>
                <th>Time</th>
                <th>Tenant</th>
                <th>Description</th>
              </tr>
            </thead>
            <tbody>
              {alerts.map(al => (
                <tr key={al.alert_id}>
                  <td>{new Date(al.created_at).toLocaleTimeString()}</td>
                  <td>{al.tenant_id}</td>
                  <td>{al.description}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

export default App
