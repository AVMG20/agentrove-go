package db

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

const timeLayout = "2006-01-02T15:04:05.000000Z07:00"

// Time is a UTC timestamp stored as RFC3339 text and serialized to JSON in the
// same ISO form the frontend already parses.
type Time struct{ time.Time }

func Now() Time { return Time{time.Now().UTC()} }

func T(t time.Time) Time { return Time{t.UTC()} }

func (t Time) Value() (driver.Value, error) {
	return t.UTC().Format(timeLayout), nil
}

func (t *Time) Scan(src any) error {
	switch v := src.(type) {
	case string:
		return t.parse(v)
	case []byte:
		return t.parse(string(v))
	case time.Time:
		t.Time = v.UTC()
		return nil
	case nil:
		t.Time = time.Time{}
		return nil
	}
	return fmt.Errorf("db.Time: unsupported type %T", src)
}

func (t *Time) parse(s string) error {
	for _, layout := range []string{timeLayout, time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if p, err := time.Parse(layout, s); err == nil {
			t.Time = p.UTC()
			return nil
		}
	}
	return fmt.Errorf("db.Time: cannot parse %q", s)
}

func (t Time) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.UTC().Format(timeLayout))
}

func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	return t.parse(s)
}

// NullTime is a nullable Time; JSON null when unset.
type NullTime struct {
	Time  Time
	Valid bool
}

func NT(t time.Time) NullTime { return NullTime{Time: T(t), Valid: true} }

func (n NullTime) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Time.Value()
}

func (n *NullTime) Scan(src any) error {
	if src == nil {
		n.Valid = false
		return nil
	}
	n.Valid = true
	return n.Time.Scan(src)
}

func (n NullTime) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return n.Time.MarshalJSON()
}

func (n *NullTime) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		n.Valid = false
		return nil
	}
	n.Valid = true
	return n.Time.UnmarshalJSON(b)
}

func (n NullTime) Ptr() *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time.Time
	return &t
}

// JSON stores any JSON-encodable value in a TEXT column. Nil encodes as SQL NULL.
type JSON[T any] struct {
	V     T
	Valid bool
}

func J[T any](v T) JSON[T] { return JSON[T]{V: v, Valid: true} }

func (j JSON[T]) Value() (driver.Value, error) {
	if !j.Valid {
		return nil, nil
	}
	b, err := json.Marshal(j.V)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (j *JSON[T]) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case nil:
		j.Valid = false
		return nil
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		return fmt.Errorf("db.JSON: unsupported type %T", src)
	}
	if len(raw) == 0 {
		j.Valid = false
		return nil
	}
	j.Valid = true
	return json.Unmarshal(raw, &j.V)
}

func (j JSON[T]) MarshalJSON() ([]byte, error) {
	if !j.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(j.V)
}

func (j *JSON[T]) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		j.Valid = false
		return nil
	}
	j.Valid = true
	return json.Unmarshal(b, &j.V)
}

// NullString helpers: *string scans/encodes NULL natively in database/sql, so
// models use *string for nullable text. These keep call sites short.
func S(s string) *string { return &s }

func StrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
