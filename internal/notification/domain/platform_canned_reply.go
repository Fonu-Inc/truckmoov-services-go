package domain

import "time"

type CannedReply struct {
	ID        string    `json:"id"`
	Shortcut  string    `json:"shortcut"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
