package infrastructure

import (
	"fmt"
	"sync"

	"github.com/truckmoov/services-go/internal/tracking/application"
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
