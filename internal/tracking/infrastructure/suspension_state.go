package infrastructure

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	redisstore "github.com/truckmoov/services-go/pkg/redis"
)

const (
	suspendedSeenTTLSeconds = 14 * 24 * 3600
	suspendedIndexKey       = "tracking:suspended-imeis"
	indexChunk              = 500
)

type RedisSuspendedSeenStore struct {
	store *redisstore.Store
}

func NewRedisSuspendedSeenStore(store *redisstore.Store) *RedisSuspendedSeenStore {
	return &RedisSuspendedSeenStore{store: store}
}

func (r *RedisSuspendedSeenStore) RecordSeen(id string, at time.Time) error {
	payload, err := json.Marshal(map[string]string{"last_seen_at": at.UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return r.store.SetEX(fmt.Sprintf("tracking:suspended-seen:%s", strings.TrimSpace(id)), payload, suspendedSeenTTLSeconds)
}

type RedisSuspendedIMEIIndex struct {
	store *redisstore.Store
}

func NewRedisSuspendedIMEIIndex(store *redisstore.Store) *RedisSuspendedIMEIIndex {
	return &RedisSuspendedIMEIIndex{store: store}
}

func (r *RedisSuspendedIMEIIndex) Members() ([]string, error) {
	return r.store.SMembers(suspendedIndexKey)
}

func (r *RedisSuspendedIMEIIndex) Add(imeis ...string) error {
	return inChunks(imeis, func(chunk []string) error { return r.store.SAdd(suspendedIndexKey, chunk...) })
}

func (r *RedisSuspendedIMEIIndex) Remove(imeis ...string) error {
	return inChunks(imeis, func(chunk []string) error { return r.store.SRem(suspendedIndexKey, chunk...) })
}

func inChunks(items []string, apply func([]string) error) error {
	for start := 0; start < len(items); start += indexChunk {
		end := min(start+indexChunk, len(items))
		if err := apply(items[start:end]); err != nil {
			return err
		}
	}
	return nil
}
