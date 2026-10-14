-- Development test data for FreeRADIUS. Safe to run many times.
--
-- How FreeRADIUS reads these tables when a user logs in:
--   radcheck       -> "check" items: things that must be TRUE to let the user in (password)
--   radreply       -> "reply" items: limits sent back to the MikroTik for ONE user
--   radusergroup   -> puts a user in a group (we use a group per PLAN)
--   radgroupreply  -> limits shared by everyone in that group/plan
--
-- MikroTik rate limit format is "upload/download" from the user's point of view:
--   "2M/5M" = 2 Mbps upload, 5 Mbps download.

BEGIN;

DELETE FROM radcheck      WHERE username LIKE 'test-%';
DELETE FROM radreply      WHERE username LIKE 'test-%';
DELETE FROM radusergroup  WHERE username LIKE 'test-%';
DELETE FROM radgroupreply WHERE groupname LIKE 'plan-%';

-- ---------- Plans (groups) ----------
-- 1 hour at 2M up / 5M down
INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES
  ('plan-1hour', 'Mikrotik-Rate-Limit', ':=', '2M/5M'),
  ('plan-1hour', 'Session-Timeout',     ':=', '3600'),
  ('plan-1hour', 'Acct-Interim-Interval', ':=', '60');

-- 500 MB data bundle at 3M up / 10M down (500 * 1024 * 1024 bytes)
INSERT INTO radgroupreply (groupname, attribute, op, value) VALUES
  ('plan-500mb', 'Mikrotik-Rate-Limit',  ':=', '3M/10M'),
  ('plan-500mb', 'Mikrotik-Total-Limit', ':=', '524288000'),
  ('plan-500mb', 'Acct-Interim-Interval', ':=', '60');

-- ---------- Test users ----------
-- test-hour / pass1234 -> on the 1-hour plan
INSERT INTO radcheck (username, attribute, op, value) VALUES
  ('test-hour', 'Cleartext-Password', ':=', 'pass1234');
INSERT INTO radusergroup (username, groupname, priority) VALUES
  ('test-hour', 'plan-1hour', 1);

-- test-data / pass1234 -> on the 500 MB plan
INSERT INTO radcheck (username, attribute, op, value) VALUES
  ('test-data', 'Cleartext-Password', ':=', 'pass1234');
INSERT INTO radusergroup (username, groupname, priority) VALUES
  ('test-data', 'plan-500mb', 1);

-- test-custom / pass1234 -> no plan, limits set directly on the user (2 minutes, 1M/1M)
INSERT INTO radcheck (username, attribute, op, value) VALUES
  ('test-custom', 'Cleartext-Password', ':=', 'pass1234');
INSERT INTO radreply (username, attribute, op, value) VALUES
  ('test-custom', 'Mikrotik-Rate-Limit', ':=', '1M/1M'),
  ('test-custom', 'Session-Timeout',     ':=', '120');

COMMIT;
