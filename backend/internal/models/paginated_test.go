package models

import (
	"encoding/json"
	"testing"
)

func TestPaginatedNilItems(t *testing.T) {
	b, _ := json.Marshal(CursorPaginated[int]{})
	if string(b) != `{"items":[],"next_cursor":null,"has_more":false}` {
		t.Fatal(string(b))
	}
	b, _ = json.Marshal(Paginated[int]{})
	if string(b) != `{"items":[],"page":0,"per_page":0,"total":0,"pages":0}` {
		t.Fatal(string(b))
	}
}
