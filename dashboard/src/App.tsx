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

  const fetchData = async () => {
    const trimmedTenant = tenantId.trim()
    if (!trimmedTenant) {
      setErrorMessage("tenant_id must not be empty")
      return
    }

    try {
      const tenantParam = encodeURIComponent(trimmedTenant)
      const evRes = await fetch(`http://localhost:8081/v1/events?tenant_id=${tenantParam}`)
      if (!evRes.ok) {
        const errorText = await evRes.text()
        throw new Error(`Events API error: HTTP ${evRes.status} (${errorText.trim() || evRes.statusText})`)
      }
      const evData = await evRes.json()
      setEvents(evData || [])

      const alRes = await fetch(`http://localhost:8081/v1/alerts?tenant_id=${tenantParam}`)
      if (!alRes.ok) {
        const errorText = await alRes.text()
        throw new Error(`Alerts API error: HTTP ${alRes.status} (${errorText.trim() || alRes.statusText})`)
      }
      const alData = await alRes.json()
      setAlerts(alData || [])

      setErrorMessage(null)
      setLastFetched(new Date().toLocaleTimeString())
    } catch (e: any) {
      console.error("Failed to fetch data:", e)
      setErrorMessage(e.message || "Failed to connect to backend API")
    }
  }

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 2000)
    return () => clearInterval(interval)
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

      {errorMessage && (
        <div style={{ padding: '10px', marginBottom: '15px', backgroundColor: '#ffebee', color: '#c62828', border: '1px solid #ef9a9a', borderRadius: '4px' }}>
          <strong>Error:</strong> {errorMessage}
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
