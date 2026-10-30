-- Development test data for FreeRADIUS. Safe to run many times.
--
-- How FreeRADIUS reads these tables when a user logs in:
--   radcheck       -> "check" items: things that must be TRUE to let the user in
--                     (password, Expiration, Max-Total-Octets)
--   radreply       -> "reply" items: limits sent back to the MikroTik for ONE user
--   radusergroup   -> puts a user in a group (we use a group per PLAN)
--   radgroupreply  -> limits shared by everyone in that group/plan
--
-- WHAT LIVES WHERE (important):
--   * A PLAN (radgroupreply) holds only things that are the same on every
--     login: the speed limit and the usage-report interval.
--   * A PURCHASE (radcheck, one username per purchase) holds the allowance:
--       Expiration       the moment the plan ends, as UNIX SECONDS
--       Max-Total-Octets total data allowed in bytes, up + down (omit = unlimited)
--     FreeRADIUS turns those into "time left" and "data left" on EVERY login, so
--     disconnecting and reconnecting never gives a user a fresh hour or fresh data.
--     Do NOT put Session-Timeout or Mikrotik-Total-Limit in a plan: they restart
--     from the full value each time the user logs in.
--
-- MikroTik rate limit format is "upload/download" from the user's point of view:
--   "2M/5M" = 2 Mbps upload, 5 Mbps download.

BEGIN;

DELETE FROM radcheck      WHERE username LIKE 'test-%';
DELETE FROM radreply      WHERE username LIKE 'test-%';
DELETE FROM radusergroup  WHERE username LIKE 'test-%';
DELETE FROM radgroupreply WHERE groupname LIKE 'dev-%';

-- ---------- Plans (groups): speed + report interval only ----------
INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES
  ('dev-1hour', 'Mikrotik-Rate-Limit',   ':=', '2M/5M'),
  ('dev-1hour', 'Acct-Interim-Interval', ':=', '60'),
  ('dev-500mb', 'Mikrotik-Rate-Limit',   ':=', '3M/10M'),
  ('dev-500mb', 'Acct-Interim-Interval', ':=', '60');

-- ---------- Test users (each stands for one purchase) ----------
-- test-hour / pass1234 -> 1-hour plan, ends one hour from now, unlimited data
INSERT INTO radcheck (username, attribute, op, value) VALUES
  ('test-hour', 'Cleartext-Password', ':=', 'pass1234'),
  ('test-hour', 'Expiration',         ':=', floor(EXTRACT(EPOCH FROM now() + interval '1 hour'))::bigint::text);
INSERT INTO radusergroup (username, groupname, priority) VALUES
  ('test-hour', 'dev-1hour', 1);

-- test-data / pass1234 -> 500 MB bundle (500 * 1024 * 1024 bytes), valid for a day
INSERT INTO radcheck (username, attribute, op, value) VALUES
  ('test-data', 'Cleartext-Password', ':=', 'pass1234'),
  ('test-data', 'Expiration',         ':=', floor(EXTRACT(EPOCH FROM now() + interval '1 day'))::bigint::text),
  ('test-data', 'Max-Total-Octets',   ':=', '524288000');
INSERT INTO radusergroup (username, groupname, priority) VALUES
  ('test-data', 'dev-500mb', 1);

-- test-custom / pass1234 -> no plan, speed set directly on the user (1M/1M), valid for a day
INSERT INTO radcheck (username, attribute, op, value) VALUES
  ('test-custom', 'Cleartext-Password', ':=', 'pass1234'),
  ('test-custom', 'Expiration',         ':=', floor(EXTRACT(EPOCH FROM now() + interval '1 day'))::bigint::text);
INSERT INTO radreply (username, attribute, op, value) VALUES
  ('test-custom', 'Mikrotik-Rate-Limit', ':=', '1M/1M');

-- test-expired / pass1234 -> plan ended an hour ago: must be rejected
INSERT INTO radcheck (username, attribute, op, value) VALUES
  ('test-expired', 'Cleartext-Password', ':=', 'pass1234'),
  ('test-expired', 'Expiration',         ':=', floor(EXTRACT(EPOCH FROM now() - interval '1 hour'))::bigint::text);

-- test-quota / pass1234 -> tiny 10 MiB allowance, used by 04-test-radius.sh to
-- prove the remaining data shrinks and the user is rejected once it is gone
INSERT INTO radcheck (username, attribute, op, value) VALUES
  ('test-quota', 'Cleartext-Password', ':=', 'pass1234'),
  ('test-quota', 'Expiration',         ':=', floor(EXTRACT(EPOCH FROM now() + interval '1 day'))::bigint::text),
  ('test-quota', 'Max-Total-Octets',   ':=', '10485760');

-- test-bigdata / pass1234 -> 10 GiB allowance, more than the 32-bit limit (4 GiB)
-- of Mikrotik-Total-Limit, so the router also needs Mikrotik-Total-Limit-Gigawords
INSERT INTO radcheck (username, attribute, op, value) VALUES
  ('test-bigdata', 'Cleartext-Password', ':=', 'pass1234'),
  ('test-bigdata', 'Expiration',         ':=', floor(EXTRACT(EPOCH FROM now() + interval '1 day'))::bigint::text),
  ('test-bigdata', 'Max-Total-Octets',   ':=', '10737418240');

COMMIT;