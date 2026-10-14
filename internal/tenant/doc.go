// Package tenant manages box owners (tenants): signup, their subdomain slug
// (kamau.yourdomain.com), and the middleware that works out which tenant a
// web request belongs to. Every other package filters its data by tenant ID.
//
// Status: planned for Phase 1.
package tenant
