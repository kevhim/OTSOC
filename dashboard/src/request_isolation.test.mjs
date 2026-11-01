import { describe, it } from 'node:test'
import assert from 'node:assert'

describe('Dashboard Tenant Request Isolation & Generation Check', () => {
  it('discards delayed responses from a previous tenant after switching', async () => {
    let committedEvents = []
    let committedAlerts = []
    let currentGen = 0
    let activeAbortController = null

    const handleTenantSwitch = (newTenant) => {
      // 1. Clear previous tenant data immediately
      committedEvents = []
      committedAlerts = []
      
      // 2. Abort in-flight requests for previous tenant
      if (activeAbortController) {
        activeAbortController.abort()
      }
      activeAbortController = new AbortController()

      // 3. Advance generation counter
      const generation = ++currentGen
      return { generation, controller: activeAbortController, tenant: newTenant }
    }

    // Step 1: User selects Tenant A
    const tenantA = handleTenantSwitch('tenant-a')
    assert.strictEqual(tenantA.generation, 1)

    // Simulate delayed response for Tenant A
    const tenantAPromise = new Promise((resolve) => {
      setTimeout(() => {
        resolve({
          events: [{ event_id: 'ev-a-1', tenant_id: 'tenant-a' }],
          alerts: [{ alert_id: 'al-a-1', tenant_id: 'tenant-a' }],
        })
      }, 50)
    })

    // Step 2: User switches to Tenant B before Tenant A response arrives
    const tenantB = handleTenantSwitch('tenant-b')
    assert.strictEqual(tenantB.generation, 2)
    assert.strictEqual(tenantA.controller.signal.aborted, true)

    // Step 3: Tenant A delayed response finally resolves
    const resA = await tenantAPromise
    if (!tenantA.controller.signal.aborted && tenantA.generation === currentGen) {
      committedEvents = resA.events
      committedAlerts = resA.alerts
    }

    // Assert: Tenant A data was discarded and never committed
    assert.deepStrictEqual(committedEvents, [])
    assert.deepStrictEqual(committedAlerts, [])

    // Step 4: Tenant B response resolves
    const resB = {
      events: [{ event_id: 'ev-b-1', tenant_id: 'tenant-b' }],
      alerts: [{ alert_id: 'al-b-1', tenant_id: 'tenant-b' }],
    }
    if (!tenantB.controller.signal.aborted && tenantB.generation === currentGen) {
      committedEvents = resB.events
      committedAlerts = resB.alerts
    }

    // Assert: Tenant B data was committed properly
    assert.strictEqual(committedEvents.length, 1)
    assert.strictEqual(committedEvents[0].tenant_id, 'tenant-b')
    assert.strictEqual(committedAlerts.length, 1)
    assert.strictEqual(committedAlerts[0].tenant_id, 'tenant-b')
  })
})
