package infrastructure

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/truckmoov/services-go/internal/tracking/application"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
)

type simKey struct{ tenantID, deviceID string }

func newSIMKey(tenantID, deviceID string) simKey {
	return simKey{strings.TrimSpace(tenantID), strings.TrimSpace(deviceID)}
}

type InMemorySIMResolver struct {
	mu      sync.RWMutex
	entries map[simKey]application.DeviceSIM
}

func NewInMemorySIMResolver() *InMemorySIMResolver {
	return &InMemorySIMResolver{entries: make(map[simKey]application.DeviceSIM)}
}

func (r *InMemorySIMResolver) Resolve(tenantID, deviceID string) (*application.DeviceSIM, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[newSIMKey(tenantID, deviceID)]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

func (r *InMemorySIMResolver) Upsert(sim application.DeviceSIM) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[newSIMKey(sim.TenantID, sim.DeviceID)] = sim
	return nil
}

func (r *InMemorySIMResolver) Evict(tenantID, deviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, newSIMKey(tenantID, deviceID))
	return nil
}

type RedisSIMResolver struct {
	store      *redisstore.Store
	ttlSeconds int
}

func NewRedisSIMResolver(store *redisstore.Store, ttlSeconds int) *RedisSIMResolver {
	return &RedisSIMResolver{store: store, ttlSeconds: ttlSeconds}
}

func (r *RedisSIMResolver) Resolve(tenantID, deviceID string) (*application.DeviceSIM, error) {
	payload, ok, err := r.store.Get(redisDeviceSIMKey(tenantID, deviceID))
	if err != nil || !ok {
		return nil, err
	}
	var sim application.DeviceSIM
	if err := json.Unmarshal(payload, &sim); err != nil {
		return nil, err
	}
	return &sim, nil
}

func (r *RedisSIMResolver) Upsert(sim application.DeviceSIM) error {
	payload, err := json.Marshal(sim)
	if err != nil {
		return fmt.Errorf("marshal device sim: %w", err)
	}
	return r.store.SetEX(redisDeviceSIMKey(sim.TenantID, sim.DeviceID), payload, r.ttlSeconds)
}

func (r *RedisSIMResolver) Evict(tenantID, deviceID string) error {
	return r.store.Del(redisDeviceSIMKey(tenantID, deviceID))
}

func redisDeviceSIMKey(tenantID, deviceID string) string {
	return fmt.Sprintf("tracking:device-sim:%s:%s", strings.TrimSpace(tenantID), strings.TrimSpace(deviceID))
}
