package infrastructure

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/truckmoov/services-go/internal/tracking/application"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
)

type InMemoryLatestLocationRepository struct {
	mu      sync.RWMutex
	entries map[string]application.LatestLocation
}

func NewInMemoryLatestLocationRepository() *InMemoryLatestLocationRepository {
	return &InMemoryLatestLocationRepository{entries: make(map[string]application.LatestLocation)}
}

func (r *InMemoryLatestLocationRepository) Save(location application.LatestLocation) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fmt.Sprintf("%s:%s", location.TenantID, location.VehicleID)
	r.entries[key] = location
	return nil
}

type RedisLatestLocationRepository struct {
	store *redisstore.Store
}

func NewRedisLatestLocationRepository(store *redisstore.Store) *RedisLatestLocationRepository {
	return &RedisLatestLocationRepository{store: store}
}

func (r *RedisLatestLocationRepository) Save(location application.LatestLocation) error {
	payload, err := json.Marshal(location)
	if err != nil {
		return fmt.Errorf("marshal latest location: %w", err)
	}
	return r.store.Set(redisLatestLocationKey(location.TenantID, location.VehicleID), payload)
}

func redisLatestLocationKey(tenantID, vehicleID string) string {
	return fmt.Sprintf("tracking:latest-location:%s:%s", tenantID, vehicleID)
}
