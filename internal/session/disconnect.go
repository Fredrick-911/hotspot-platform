package session

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/Fredrick-911/hotspot-platform/internal/radius"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RadiusDisconnector sends RADIUS Disconnect-Requests to the router that holds
// a session. It finds the router (and its secret) in the devices table by the
// IP address that appeared in accounting.
type RadiusDisconnector struct {
	DB     *pgxpool.Pool
	Client radius.Client
}

// Disconnect implements Disconnector. A router that says "no such session"
// counts as success: the user is already gone.
func (d RadiusDisconnector) Disconnect(ctx context.Context, s Online) error {
	var secret string
	err := d.DB.QueryRow(ctx,
		`SELECT radius_secret FROM devices WHERE nas_ip_address = $1::inet LIMIT 1`, s.NASIP).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("no registered router with IP %s", s.NASIP)
	}
	if err != nil {
		return fmt.Errorf("look up router secret: %w", err)
	}

	err = d.Client.Disconnect(ctx, radius.Request{
		Addr:      s.NASIP,
		Secret:    secret,
		Username:  s.Username,
		SessionID: s.SessionID,
		NASIP:     net.ParseIP(s.NASIP),
	})
	if radius.IsSessionGone(err) {
		return nil
	}
	return err
}
