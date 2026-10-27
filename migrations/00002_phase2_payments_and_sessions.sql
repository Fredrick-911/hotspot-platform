-- migrations/00002_phase2_payments_and_sessions.sql

-- 1. PAYMENTS TABLE
CREATE TABLE IF NOT EXISTS payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    phone_number VARCHAR(15) NOT NULL,
    amount_cents INTEGER NOT NULL,
    external_reference VARCHAR(64) UNIQUE NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING', -- PENDING, SUCCESS, FAILED
    mpesa_receipt VARCHAR(32),
    result_desc TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 2. SESSIONS TABLE
CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    payment_id UUID NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    phone_number VARCHAR(15) NOT NULL,
    mac_address VARCHAR(17),
    username VARCHAR(64) NOT NULL,
    password VARCHAR(64) NOT NULL,
    duration_minutes INTEGER NOT NULL,
    starts_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE', -- ACTIVE, EXPIRED, REVOKED
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for high-speed lookups
CREATE INDEX IF NOT EXISTS idx_payments_reference ON payments(external_reference);
CREATE INDEX IF NOT EXISTS idx_payments_tenant_id ON payments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_sessions_lookup ON sessions(tenant_id, phone_number, status);
CREATE INDEX IF NOT EXISTS idx_sessions_mac ON sessions(mac_address);

-- Grant privileges to the radius user
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO radius;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO radius;