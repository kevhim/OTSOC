import { describe, it } from 'node:test'
import assert from 'node:assert'
import { fetchTenantData } from './tenant_query.ts'

describe('Dashboard Tenant Request Isolation & Generation Check', () => {
  it('exercises shared fetchTenantData logic and discards delayed responses from a previous tenant after switching', async () => {
    let committedEvents = []
    let committedAlerts = []
    let currentGen = 0
    let activeAbortController = null

    // Mock fetchFn simulating delayed network responses
    const mockFetch = async (url, options) => {
      if (url.includes('tenant-a')) {
        // Simulate delayed response for Tenant A
        await new Promise((resolve) => setTimeout(resolve, 50))
        if (options.signal.aborted) {
          const err = new Error('The operation was aborted')
          err.name = 'AbortError'
          throw err
        }
        return {
          ok: true,
          status: 200,
          json: async () => [{ event_id: 'ev-a-1', tenant_id: 'tenant-a' }],
        }
      }

      if (url.includes('tenant-b')) {
        return {
          ok: true,
          status: 200,
          json: async () => [{ event_id: 'ev-b-1', tenant_id: 'tenant-b' }],
        }
      }

      throw new Error('Unknown URL: ' + url)
    }

    const switchTenant = (newTenant) => {
      // 1. Reset state
      committedEvents = []
      committedAlerts = []

      // 2. Abort previous controller
      if (activeAbortController) {
        activeAbortController.abort()
      }
      activeAbortController = new AbortController()

      // 3. Advance generation
      const generation = ++currentGen
      return { generation, controller: activeAbortController, tenant: newTenant }
    }

    // Step 1: Select Tenant A
    const tenantA = switchTenant('tenant-a')
    assert.strictEqual(tenantA.generation, 1)

    // Trigger shared fetchTenantData for Tenant A
    const promiseA = fetchTenantData({
      apiBase: '',
      tenantId: tenantA.tenant,
      generation: tenantA.generation,
      signal: tenantA.controller.signal,
      isCurrentGeneration: (gen) => gen === currentGen,
      fetchFn: mockFetch,
      onSuccess: ({ events, alerts }) => {
        committedEvents = events
        committedAlerts = alerts
      },
      onError: () => {},
    })

    // Step 2: Switch immediately to Tenant B before Tenant A resolves
    const tenantB = switchTenant('tenant-b')
    assert.strictEqual(tenantB.generation, 2)
    assert.strictEqual(tenantA.controller.signal.aborted, true)

    // Trigger shared fetchTenantData for Tenant B
    const promiseB = fetchTenantData({
      apiBase: '',
      tenantId: tenantB.tenant,
      generation: tenantB.generation,
      signal: tenantB.controller.signal,
      isCurrentGeneration: (gen) => gen === currentGen,
      fetchFn: mockFetch,
      onSuccess: ({ events, alerts }) => {
        committedEvents = events
        committedAlerts = alerts
      },
      onError: () => {},
    })

    // Wait for both to finish
    await Promise.all([promiseA, promiseB])

    // Assert: Only Tenant B data was committed; Tenant A was discarded by the shared logic
    assert.strictEqual(committedEvents.length, 1)
    assert.strictEqual(committedEvents[0].tenant_id, 'tenant-b')
    assert.strictEqual(committedAlerts.length, 1)
    assert.strictEqual(committedAlerts[0].tenant_id, 'tenant-b')
  })
})
