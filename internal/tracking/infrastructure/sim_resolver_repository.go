package infrastructure

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/truckmoov/services-go/internal/tracking/application"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
)

type InMemorySIMResolver struct {
	mu      sync.RWMutex
	entries map[string]application.DeviceSIM
}

func NewInMemorySIMResolver() *InMemorySIMResolver {
	return &InMemorySIMResolver{entries: make(map[string]application.DeviceSIM)}
}

func (r *InMemorySIMResolver) Resolve(deviceID string) (*application.DeviceSIM, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[strings.TrimSpace(deviceID)]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

func (r *InMemorySIMResolver) Upsert(sim application.DeviceSIM) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[strings.TrimSpace(sim.DeviceID)] = sim
	return nil
}

func (r *InMemorySIMResolver) Evict(deviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, strings.TrimSpace(deviceID))
	return nil
}

type RedisSIMResolver struct {
	store      *redisstore.Store
	ttlSeconds int
}

func NewRedisSIMResolver(store *redisstore.Store, ttlSeconds int) *RedisSIMResolver {
	return &RedisSIMResolver{store: store, ttlSeconds: ttlSeconds}
}

func (r *RedisSIMResolver) Resolve(deviceID string) (*application.DeviceSIM, error) {
	payload, ok, err := r.store.Get(redisDeviceSIMKey(deviceID))
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
	return r.store.SetEX(redisDeviceSIMKey(sim.DeviceID), payload, r.ttlSeconds)
}

func (r *RedisSIMResolver) Evict(deviceID string) error {
	return r.store.Del(redisDeviceSIMKey(deviceID))
}

func redisDeviceSIMKey(deviceID string) string {
	return fmt.Sprintf("tracking:device-sim:%s", strings.TrimSpace(deviceID))
}
