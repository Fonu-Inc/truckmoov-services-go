package infrastructure

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

func (r *InMemoryLatestLocationRepository) Get(tenantID, vehicleID string) (*application.LatestLocation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := fmt.Sprintf("%s:%s", tenantID, vehicleID)
	entry, ok := r.entries[key]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

func (r *InMemoryLatestLocationRepository) List(tenantID string) ([]application.LatestLocation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make([]application.LatestLocation, 0, len(r.entries))
	for _, entry := range r.entries {
		if entry.TenantID != tenantID {
			continue
		}
		results = append(results, entry)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].VehicleID < results[j].VehicleID
	})
	return results, nil
}

type RedisLatestLocationRepository struct {
	store      *redisstore.Store
	ttlSeconds int
}

func NewRedisLatestLocationRepository(store *redisstore.Store, ttlSeconds int) *RedisLatestLocationRepository {
	return &RedisLatestLocationRepository{store: store, ttlSeconds: ttlSeconds}
}

func (r *RedisLatestLocationRepository) Save(location application.LatestLocation) error {
	payload, err := json.Marshal(location)
	if err != nil {
		return fmt.Errorf("marshal latest location: %w", err)
	}
	if err := r.store.SetEX(redisLatestLocationKey(location.TenantID, location.VehicleID), payload, r.ttlSeconds); err != nil {
		return err
	}
	return r.store.SAdd(redisLatestLocationIndexKey(location.TenantID), location.VehicleID)
}

func (r *RedisLatestLocationRepository) Get(tenantID, vehicleID string) (*application.LatestLocation, error) {
	payload, ok, err := r.store.Get(redisLatestLocationKey(tenantID, vehicleID))
	if err != nil || !ok {
		return nil, err
	}
	entry, err := unmarshalLatestLocation(payload)
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (r *RedisLatestLocationRepository) List(tenantID string) ([]application.LatestLocation, error) {
	vehicleIDs, err := r.store.SMembers(redisLatestLocationIndexKey(tenantID))
	if err != nil {
		return nil, err
	}
	results := make([]application.LatestLocation, 0, len(vehicleIDs))
	for _, vehicleID := range vehicleIDs {
		payload, ok, err := r.store.Get(redisLatestLocationKey(tenantID, vehicleID))
		if err != nil || !ok {
			continue
		}
		entry, err := unmarshalLatestLocation(payload)
		if err != nil {
			continue
		}
		results = append(results, entry)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].VehicleID < results[j].VehicleID
	})
	return results, nil
}

func redisLatestLocationKey(tenantID, vehicleID string) string {
	return fmt.Sprintf("tracking:latest-location:%s:%s", tenantID, vehicleID)
}

func redisLatestLocationIndexKey(tenantID string) string {
	return fmt.Sprintf("tracking:latest-location:index:%s", strings.TrimSpace(tenantID))
}

func unmarshalLatestLocation(payload []byte) (application.LatestLocation, error) {
	var entry application.LatestLocation
	if err := json.Unmarshal(payload, &entry); err != nil {
		return application.LatestLocation{}, err
	}
	return entry, nil
}
