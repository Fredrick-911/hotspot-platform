-- migrations/00005_wireguard_keys.sql
-- Store per-router WireGuard keypair for zero-touch peer syncing.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS wg_public_key TEXT;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS wg_private_key TEXT;
