package db

import "context"

type Client struct {
	DSN string
}

func NewClient(dsn string) *Client {
	return &Client{DSN: dsn}
}

func (c *Client) Ping(_ context.Context) error {
	// Placeholder for real DB connectivity.
	return nil
}
