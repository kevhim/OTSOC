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

  const fetchData = async () => {
    try {
      const evRes = await fetch('http://localhost:8081/v1/events')
      if (evRes.ok) setEvents(await evRes.json())
      
      const alRes = await fetch('http://localhost:8081/v1/alerts')
      if (alRes.ok) setAlerts(await alRes.json())
    } catch (e) {
      console.error("Failed to fetch data:", e)
    }
  }

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 2000)
    return () => clearInterval(interval)
  }, [])

  return (
    <div style={{ padding: '20px', fontFamily: 'sans-serif' }}>
      <h1>RedCyberFox Dashboard (Phase 1 Vertical Slice)</h1>
      
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
