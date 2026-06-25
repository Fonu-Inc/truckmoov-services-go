package infrastructure

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/truckmoov/services-go/internal/tracking/application"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
)

type InMemoryIMEIResolver struct {
	mu      sync.RWMutex
	entries map[string]application.DeviceIdentity
}

func NewInMemoryIMEIResolver() *InMemoryIMEIResolver {
	return &InMemoryIMEIResolver{entries: make(map[string]application.DeviceIdentity)}
}

func (r *InMemoryIMEIResolver) Resolve(imei string) (*application.DeviceIdentity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[strings.TrimSpace(imei)]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

func (r *InMemoryIMEIResolver) Upsert(identity application.DeviceIdentity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[strings.TrimSpace(identity.IMEI)] = identity
	return nil
}

func (r *InMemoryIMEIResolver) Evict(imei string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, strings.TrimSpace(imei))
	return nil
}

type RedisIMEIResolver struct {
	store      *redisstore.Store
	ttlSeconds int
}

func NewRedisIMEIResolver(store *redisstore.Store, ttlSeconds int) *RedisIMEIResolver {
	return &RedisIMEIResolver{store: store, ttlSeconds: ttlSeconds}
}

func (r *RedisIMEIResolver) Resolve(imei string) (*application.DeviceIdentity, error) {
	payload, ok, err := r.store.Get(redisIMEIIdentityKey(imei))
	if err != nil || !ok {
		return nil, err
	}
	var identity application.DeviceIdentity
	if err := json.Unmarshal(payload, &identity); err != nil {
		return nil, err
	}
	return &identity, nil
}

func (r *RedisIMEIResolver) Upsert(identity application.DeviceIdentity) error {
	payload, err := json.Marshal(identity)
	if err != nil {
		return fmt.Errorf("marshal device identity: %w", err)
	}
	return r.store.SetEX(redisIMEIIdentityKey(identity.IMEI), payload, r.ttlSeconds)
}

func (r *RedisIMEIResolver) Evict(imei string) error {
	return r.store.Del(redisIMEIIdentityKey(imei))
}

func redisIMEIIdentityKey(imei string) string {
	return fmt.Sprintf("tracking:imei-identity:%s", strings.TrimSpace(imei))
}
