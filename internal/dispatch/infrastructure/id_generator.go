package infrastructure

import (
	"fmt"
	"time"
)

type TimeIDGenerator struct{}

func (TimeIDGenerator) New() string {
	return fmt.Sprintf("evt-%d", time.Now().UnixNano())
}
