package infrastructure

import (
	"context"
	"time"

	redisstore "github.com/truckmoov/services-go/pkg/redis"
)

const eventClaimPrefix = "notification:event-claim:"

type RedisEventClaims struct {
	store *redisstore.Store
}

func NewRedisEventClaims(store *redisstore.Store) *RedisEventClaims {
	return &RedisEventClaims{store: store}
}

func (c *RedisEventClaims) Claim(_ context.Context, key string, ttl time.Duration) (bool, error) {
	return c.store.SetNX(eventClaimPrefix+key, []byte("1"), int(ttl.Seconds()))
}

func (c *RedisEventClaims) Release(_ context.Context, key string) error {
	return c.store.Del(eventClaimPrefix + key)
}
