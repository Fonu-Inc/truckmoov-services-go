package infrastructure

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/truckmoov/services-go/internal/tracking/application"
	redisstore "github.com/truckmoov/services-go/pkg/redis"
)

const maxHistoryEntries = 200

type InMemoryLocationHistoryRepository struct {
	mu      sync.RWMutex
	entries map[string][]application.LatestLocation
}

func NewInMemoryLocationHistoryRepository() *InMemoryLocationHistoryRepository {
	return &InMemoryLocationHistoryRepository{entries: make(map[string][]application.LatestLocation)}
}

func (r *InMemoryLocationHistoryRepository) Append(entry application.LatestLocation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fmt.Sprintf("%s:%s", entry.TenantID, entry.VehicleID)
	list := append([]application.LatestLocation{entry}, r.entries[key]...)
	if len(list) > maxHistoryEntries {
		list = list[:maxHistoryEntries]
	}
	r.entries[key] = list
	return nil
}

func (r *InMemoryLocationHistoryRepository) History(tenantID, vehicleID string, limit int) ([]application.LatestLocation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := fmt.Sprintf("%s:%s", tenantID, vehicleID)
	list := append([]application.LatestLocation{}, r.entries[key]...)
	if limit <= 0 || limit > len(list) {
		limit = len(list)
	}
	if limit == 0 {
		return nil, nil
	}
	return append([]application.LatestLocation(nil), list[:limit]...), nil
}

type RedisLocationHistoryRepository struct {
	store      *redisstore.Store
	ttlSeconds int
}

func NewRedisLocationHistoryRepository(store *redisstore.Store, ttlSeconds int) *RedisLocationHistoryRepository {
	return &RedisLocationHistoryRepository{store: store, ttlSeconds: ttlSeconds}
}

func (r *RedisLocationHistoryRepository) Append(entry application.LatestLocation) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal history entry: %w", err)
	}
	return r.store.PushCapped(redisLocationHistoryKey(entry.TenantID, entry.VehicleID), data, maxHistoryEntries, r.ttlSeconds)
}

func (r *RedisLocationHistoryRepository) History(tenantID, vehicleID string, limit int) ([]application.LatestLocation, error) {
	if limit <= 0 || limit > maxHistoryEntries {
		limit = maxHistoryEntries
	}
	raw, err := r.store.Range(redisLocationHistoryKey(tenantID, vehicleID), limit)
	if err != nil {
		return nil, err
	}
	history := make([]application.LatestLocation, 0, len(raw))
	for _, payload := range raw {
		var entry application.LatestLocation
		if json.Unmarshal(payload, &entry) == nil {
			history = append(history, entry)
		}
	}
	return history, nil
}

func redisLocationHistoryKey(tenantID, vehicleID string) string {
	return fmt.Sprintf("tracking:location-history:v2:%s:%s", tenantID, vehicleID)
}
