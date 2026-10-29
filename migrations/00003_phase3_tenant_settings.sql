-- migrations/00003_phase3_tenant_settings.sql
ALTER TABLE tenants 
ADD COLUMN IF NOT EXISTS support_phone VARCHAR(20) DEFAULT '',
ADD COLUMN IF NOT EXISTS payhero_channel_id INT DEFAULT 0;