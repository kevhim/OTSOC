ALTER TABLE alerts ADD CONSTRAINT uq_alerts_tenant_event UNIQUE (tenant_id, event_id);
