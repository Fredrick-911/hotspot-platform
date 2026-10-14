// Package session turns a successful payment or voucher into internet access:
// it writes the user into the FreeRADIUS tables (radcheck, radusergroup),
// reads usage from radacct, and decides when a session has expired.
//
// Status: planned for Phase 2.
package session
