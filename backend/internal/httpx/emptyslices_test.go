package httpx

import (
	"encoding/json"
	"testing"
	"time"
)

type inner struct {
	Tags []string `json:"tags"`
	Raw  []byte   `json:"raw"`
}

type outer struct {
	Items  []*inner       `json:"items"`
	Nested inner          `json:"nested"`
	Ptr    *inner         `json:"ptr"`
	Map    map[string]any `json:"map"`
	When   time.Time      `json:"-"`
	hidden []string
}

func TestEmptySlices(t *testing.T) {
	in := outer{Items: []*inner{{}}, Ptr: &inner{}, Map: map[string]any{"a": []string(nil), "b": nil}}
	b, err := json.Marshal(emptySlices(in))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"items":[{"tags":[],"raw":null}],"nested":{"tags":[],"raw":null},"ptr":{"tags":[],"raw":null},"map":{"a":[],"b":null}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	if in.Ptr.Tags != nil {
		t.Fatal("input mutated")
	}
	b, _ = json.Marshal(emptySlices([]int(nil)))
	if string(b) != `[]` {
		t.Fatal(string(b))
	}
	b, _ = json.Marshal(emptySlices(map[string]any{"x": 1}))
	if string(b) != `{"x":1}` {
		t.Fatal(string(b))
	}
}
