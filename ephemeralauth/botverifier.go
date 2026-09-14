package ephemeralauth

import "context"

// BotVerifier verifies bot-protection tokens from the client.
// Implementations must be fail-closed: return (false, err) or (false, nil)
// to reject; (true, nil) to accept.
type BotVerifier interface {
	Verify(ctx context.Context, token string, remoteIP string) (bool, error)
}
