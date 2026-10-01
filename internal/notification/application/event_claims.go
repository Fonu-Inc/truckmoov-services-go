package application

import (
	"context"
	"time"
)

// eventClaimTTL outlives the stream's retention, so a captured event cannot be
// replayed once its claim has expired while the broker still holds it.
const eventClaimTTL = 30 * 24 * time.Hour

// EventClaimStore remembers which sends an event has already caused.
type EventClaimStore interface {
	// Claim returns false when the key was already claimed.
	Claim(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, key string) error
}

// sendOnce runs send at most once per key. A failed send gives the claim back,
// so a redelivery retries only what did not go out.
func sendOnce(ctx context.Context, claims EventClaimStore, key string, send func() error) (bool, error) {
	claimed, err := claims.Claim(ctx, key, eventClaimTTL)
	if err != nil || !claimed {
		return false, err
	}
	if sendErr := send(); sendErr != nil {
		_ = claims.Release(ctx, key)
		return false, sendErr
	}
	return true, nil
}
