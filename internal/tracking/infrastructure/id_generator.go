package infrastructure

import (
	"fmt"
	"time"
)

type TimeIDGenerator struct{}

func (TimeIDGenerator) New(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
