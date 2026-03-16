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
	key := redisLocationHistoryKey(entry.TenantID, entry.VehicleID)
	payload, ok, err := r.store.Get(key)
	if err != nil {
		return err
	}
	var history []application.LatestLocation
	if ok && payload != nil {
		_ = json.Unmarshal(payload, &history)
	}
	history = append([]application.LatestLocation{entry}, history...)
	if len(history) > maxHistoryEntries {
		history = history[:maxHistoryEntries]
	}
	data, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}
	return r.store.SetEX(key, data, r.ttlSeconds)
}

func (r *RedisLocationHistoryRepository) History(tenantID, vehicleID string, limit int) ([]application.LatestLocation, error) {
	key := redisLocationHistoryKey(tenantID, vehicleID)
	payload, ok, err := r.store.Get(key)
	if err != nil || !ok || payload == nil {
		return nil, nil
	}
	var history []application.LatestLocation
	if err := json.Unmarshal(payload, &history); err != nil {
		return nil, nil
	}
	if limit <= 0 || limit > len(history) {
		limit = len(history)
	}
	if limit == 0 {
		return nil, nil
	}
	return append([]application.LatestLocation(nil), history[:limit]...), nil
}

func redisLocationHistoryKey(tenantID, vehicleID string) string {
	return fmt.Sprintf("tracking:location-history:%s:%s", tenantID, vehicleID)
}
