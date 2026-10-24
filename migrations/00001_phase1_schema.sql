-- migrations/00001_phase1_schema.sql

-- Enable UUID generation
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- 1. TENANTS: The box owners (ISPs)
CREATE TABLE IF NOT EXISTS tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    subdomain VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 2. PLANS: The internet packages they sell
CREATE TABLE IF NOT EXISTS plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,           -- e.g., "1 Hour Fast"
    price_cents INTEGER NOT NULL,         -- Stored in cents/smallest currency unit to avoid float errors
    duration_minutes INTEGER NOT NULL,    -- e.g., 60
    data_limit_bytes BIGINT,              -- null means unlimited data
    speed_limit VARCHAR(50),              -- e.g., "2M/5M" (MikroTik format)
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 3. DEVICES: The MikroTik routers connected to this platform
CREATE TABLE IF NOT EXISTS devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mac_address VARCHAR(17) UNIQUE NOT NULL,
    nas_ip_address INET,                  -- For RADIUS incoming requests
    radius_secret VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for fast lookups
CREATE INDEX IF NOT EXISTS idx_plans_tenant_id ON plans(tenant_id);
CREATE INDEX IF NOT EXISTS idx_devices_tenant_id ON devices(tenant_id);
-- Grant permissions to the radius user
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO radius;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO radius;
