package httpx

import (
	"net/url"
	"testing"
)

func TestParsePageRequest(t *testing.T) {
	got, err := ParsePageRequest(url.Values{})
	if err != nil || got.Page != 1 || got.Limit != DefaultPageSize {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	for _, bad := range []url.Values{{"page": {"0"}}, {"page": {"x"}}, {"limit": {"-1"}}, {"limit": {"101"}}} {
		if _, err := ParsePageRequest(bad); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
}

func TestPaginate(t *testing.T) {
	items := make([]int, 45)
	for i := range items {
		items[i] = i + 1
	}
	page, meta := Paginate(items, PageRequest{Page: 3, Limit: 20})
	if len(page) != 5 || page[0] != 41 || meta.Total != 45 || meta.TotalPages != 3 {
		t.Fatalf("last page: %v %+v", page, meta)
	}
	page, meta = Paginate(items, PageRequest{Page: 9, Limit: 20})
	if len(page) != 0 || meta.Total != 45 {
		t.Fatalf("past end: %v %+v", page, meta)
	}
	if _, meta = Paginate([]int{}, PageRequest{Page: 1, Limit: 20}); meta.TotalPages != 1 {
		t.Fatalf("empty: %+v", meta)
	}
}
