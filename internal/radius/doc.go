// Package radius sends RADIUS messages from Go directly to MikroTik routers:
// Disconnect-Request (kick a user now) and CoA-Request (change a user's speed
// mid-session), on UDP port 3799.
//
// FreeRADIUS itself handles logins and accounting; this package only covers
// messages that the platform starts.
//
// Status: planned for Phase 2.
package radius
