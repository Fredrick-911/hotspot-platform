-- migrations/00004_intasend_billing.sql
-- IntaSend B2B Billing (SaaS subscriptions & tenant billing tracking)
-- Safe to run more than once.

-- 1. TENANTS: subscription expiration timestamp and monthly rate (default KES 2,500)
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS subscription_expires_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS subscription_amount_kes INTEGER NOT NULL DEFAULT 2500;

-- 2. PLATFORM_INVOICES: stores IntaSend billing records for box owners
CREATE TABLE IF NOT EXISTS platform_invoices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    invoice_id VARCHAR(100) NOT NULL UNIQUE,
    amount_kes INTEGER NOT NULL,
    provider VARCHAR(50) NOT NULL DEFAULT 'INTASEND',
    account VARCHAR(50),
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    provider_ref VARCHAR(100),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_platform_invoices_tenant_id ON platform_invoices(tenant_id);
CREATE INDEX IF NOT EXISTS idx_platform_invoices_invoice_id ON platform_invoices(invoice_id);
