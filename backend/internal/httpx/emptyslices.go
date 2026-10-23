package httpx

import (
	"encoding/json"
	"reflect"
)

var marshalerType = reflect.TypeFor[json.Marshaler]()

// emptySlices returns v with every nil slice replaced by an empty one, so
// list fields encode as [] (the frontend iterates them without null checks).
// Values that marshal themselves are left alone.
func emptySlices(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if !needsFix(rv.Type(), map[reflect.Type]bool{}) {
		return v
	}
	return fixValue(rv, 0).Interface()
}

// needsFix reports whether a value of type t can contain a slice we'd fix.
func needsFix(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	if t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType) {
		return false
	}
	switch t.Kind() {
	case reflect.Slice:
		return t.Elem().Kind() != reflect.Uint8
	case reflect.Interface:
		return true
	case reflect.Pointer, reflect.Array, reflect.Map:
		return needsFix(t.Elem(), seen)
	case reflect.Struct:
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() && needsFix(f.Type, seen) {
				return true
			}
		}
	}
	return false
}

func fixValue(v reflect.Value, depth int) reflect.Value {
	if depth > 64 || !v.IsValid() {
		return v
	}
	t := v.Type()
	if t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType) {
		return v
	}
	switch t.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		inner := fixValue(v.Elem(), depth+1)
		out := reflect.New(t).Elem()
		out.Set(inner)
		return out
	case reflect.Pointer:
		if v.IsNil() || !needsFix(t.Elem(), map[reflect.Type]bool{}) {
			return v
		}
		out := reflect.New(t.Elem())
		out.Elem().Set(fixValue(v.Elem(), depth+1))
		return out
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return v
		}
		if v.IsNil() {
			return reflect.MakeSlice(t, 0, 0)
		}
		if !needsFix(t.Elem(), map[reflect.Type]bool{}) {
			return v
		}
		out := reflect.MakeSlice(t, v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(fixValue(v.Index(i), depth+1))
		}
		return out
	case reflect.Map:
		if v.IsNil() || !needsFix(t.Elem(), map[reflect.Type]bool{}) {
			return v
		}
		out := reflect.MakeMapWithSize(t, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), fixValue(iter.Value(), depth+1))
		}
		return out
	case reflect.Struct:
		if !needsFix(t, map[reflect.Type]bool{}) {
			return v
		}
		out := reflect.New(t).Elem()
		out.Set(v)
		for i := range t.NumField() {
			if t.Field(i).IsExported() {
				out.Field(i).Set(fixValue(v.Field(i), depth+1))
			}
		}
		return out
	}
	return v
}
