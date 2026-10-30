-- migrations/00003_dashboards.sql
-- Dashboards (admin, tenant owner, customer) + payment/session hardening.
-- Safe to run more than once.

-- 1. USERS: staff who log in to a dashboard.
--    role 'admin' = platform operator (tenant_id is NULL)
--    role 'owner' = a box owner, always tied to one tenant
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    role VARCHAR(20) NOT NULL CHECK (role IN ('admin', 'owner')),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    last_login_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT users_role_tenant CHECK (
        (role = 'admin' AND tenant_id IS NULL) OR (role = 'owner' AND tenant_id IS NOT NULL)
    )
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_users_email ON users (lower(email));
CREATE INDEX IF NOT EXISTS idx_users_tenant_id ON users (tenant_id);

-- 2. TENANTS: can be suspended; platform commission is stored per tenant.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS commission_bps INTEGER NOT NULL DEFAULT 1000; -- 1000 = 10.00 %
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS contact_phone VARCHAR(20);
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_status_check;
ALTER TABLE tenants ADD CONSTRAINT tenants_status_check CHECK (status IN ('ACTIVE', 'SUSPENDED'));
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_commission_check;
ALTER TABLE tenants ADD CONSTRAINT tenants_commission_check CHECK (commission_bps BETWEEN 0 AND 10000);

-- 3. PLANS: archive instead of delete, so payment history is never lost.
ALTER TABLE plans ADD COLUMN IF NOT EXISTS active BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE plans ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP;

-- Deleting a plan used to CASCADE and silently delete its payments and sessions.
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_plan_id_fkey;
ALTER TABLE payments ADD CONSTRAINT payments_plan_id_fkey
    FOREIGN KEY (plan_id) REFERENCES plans(id) ON DELETE RESTRICT;
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_plan_id_fkey;
ALTER TABLE sessions ADD CONSTRAINT sessions_plan_id_fkey
    FOREIGN KEY (plan_id) REFERENCES plans(id) ON DELETE RESTRICT;

-- 4. PAYMENTS: the platform fee is recorded when the payment succeeds, so
--    changing a tenant's commission later never rewrites old revenue.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS fee_cents INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS mac_address VARCHAR(17); -- the device that paid, if the router told us
CREATE INDEX IF NOT EXISTS idx_payments_tenant_created ON payments (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_payments_status_created ON payments (status, created_at DESC);

-- 5. SESSIONS: snapshot what was bought, so editing a plan later never changes
--    a pass someone already paid for. One session per payment.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS data_limit_bytes BIGINT;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS speed_limit VARCHAR(50);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_sessions_payment ON sessions (payment_id);
CREATE INDEX IF NOT EXISTS idx_sessions_username ON sessions (username);
CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions (status, expires_at);

-- 6. DEVICES: a friendly name, and a heartbeat so the dashboard can show "online".
ALTER TABLE devices ADD COLUMN IF NOT EXISTS name VARCHAR(100) NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_devices_nas_ip ON devices (nas_ip_address);

-- 7. ANNOUNCEMENTS: short messages an owner pushes to everyone on their WiFi.
CREATE TABLE IF NOT EXISTS announcements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    message VARCHAR(280) NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_announcements_tenant ON announcements (tenant_id, created_at DESC);

-- 8. radacct is created by FreeRADIUS's own schema, not by us. Add the index
--    the dashboards need ("data used by this username") when the table exists.
DO $$
BEGIN
    IF to_regclass('public.radacct') IS NOT NULL THEN
        CREATE INDEX IF NOT EXISTS idx_radacct_username ON radacct (username);
    END IF;
END $$;

GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO radius;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO radius;