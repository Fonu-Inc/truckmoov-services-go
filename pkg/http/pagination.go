package httpx

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

type PageMeta struct {
	Total      int `json:"total"`
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	TotalPages int `json:"totalPages"`
}

type PageRequest struct {
	Page  int
	Limit int
}

// ParsePageRequest reads page and limit from the query. An absent value takes
// its default; a malformed or out-of-range one is an error for the caller to
// answer 400, matching the TS services.
func ParsePageRequest(query url.Values) (PageRequest, error) {
	page, err := positiveIntParam(query, "page", 1)
	if err != nil {
		return PageRequest{}, err
	}
	limit, err := positiveIntParam(query, "limit", DefaultPageSize)
	if err != nil {
		return PageRequest{}, err
	}
	if limit > MaxPageSize {
		return PageRequest{}, fmt.Errorf("limit must not be greater than %d", MaxPageSize)
	}
	return PageRequest{Page: page, Limit: limit}, nil
}

func positiveIntParam(query url.Values, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(query.Get(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func Paginate[T any](items []T, request PageRequest) ([]T, PageMeta) {
	total := len(items)
	start := clampInt((request.Page-1)*request.Limit, total)
	end := clampInt(start+request.Limit, total)
	totalPages := (total + request.Limit - 1) / request.Limit
	if totalPages < 1 {
		totalPages = 1
	}
	return items[start:end], PageMeta{
		Total:      total,
		Page:       request.Page,
		Limit:      request.Limit,
		TotalPages: totalPages,
	}
}

func clampInt(value, ceiling int) int {
	if value > ceiling {
		return ceiling
	}
	return value
}
