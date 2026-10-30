package gltest

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/goccy/go-json"
)

// getJSONPath resolves a dot-delimited path (e.g. "data.0.title") within unmarshaled JSON data.
func getJSONPath(val any, path string) (any, bool) {
	if path == "" {
		return val, true
	}

	parts := strings.Split(path, ".")
	curr := val

	for _, part := range parts {
		if curr == nil {
			return nil, false
		}

		switch v := curr.(type) {
		case map[string]any:
			next, ok := v[part]
			if !ok {
				return nil, false
			}
			curr = next

		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil, false
			}
			curr = v[idx]

		default:
			// Attempt to reflect if struct or custom map
			rVal := reflect.ValueOf(curr)
			if rVal.Kind() == reflect.Map {
				mapKey := reflect.ValueOf(part)
				mapVal := rVal.MapIndex(mapKey)
				if !mapVal.IsValid() {
					return nil, false
				}
				curr = mapVal.Interface()
			} else if rVal.Kind() == reflect.Slice || rVal.Kind() == reflect.Array {
				idx, err := strconv.Atoi(part)
				if err != nil || idx < 0 || idx >= rVal.Len() {
					return nil, false
				}
				curr = rVal.Index(idx).Interface()
			} else {
				return nil, false
			}
		}
	}

	return curr, true
}

// isSubset checks if expected is a subset of actual.
func isSubset(actual, expected any) bool {
	// Normalize numbers if different types (e.g. float64 from json unmarshal vs int from literal)
	actual = normalizeJSONValue(actual)
	expected = normalizeJSONValue(expected)

	if expected == nil {
		return actual == nil
	}

	switch exp := expected.(type) {
	case map[string]any:
		act, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, expVal := range exp {
			actVal, exists := act[k]
			if !exists {
				return false
			}
			if !isSubset(actVal, expVal) {
				return false
			}
		}
		return true

	case []any:
		act, ok := actual.([]any)
		if !ok || len(act) < len(exp) {
			return false
		}
		for i, expVal := range exp {
			if !isSubset(act[i], expVal) {
				return false
			}
		}
		return true

	default:
		return reflect.DeepEqual(actual, expected)
	}
}

// normalizeJSONValue standardizes numeric types and maps into json-compatible types.
func normalizeJSONValue(v any) any {
	if v == nil {
		return nil
	}

	// Fast path roundtrip via json to match encoding/json / goccy types
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}

	var res any
	if err := json.Unmarshal(data, &res); err != nil {
		return v
	}
	return res
}

// valuesEqual checks for equality, supporting predicate matching functions.
func valuesEqual(a, b any) bool {
	if b == nil {
		return a == nil
	}

	// Check if b is a predicate matcher func(T) bool
	bVal := reflect.ValueOf(b)
	if bVal.Kind() == reflect.Func && bVal.Type().NumIn() == 1 && bVal.Type().NumOut() == 1 && bVal.Type().Out(0).Kind() == reflect.Bool {
		aVal := reflect.ValueOf(a)
		paramType := bVal.Type().In(0)
		if aVal.IsValid() && aVal.Type().AssignableTo(paramType) {
			res := bVal.Call([]reflect.Value{aVal})
			return res[0].Bool()
		} else if paramType.Kind() == reflect.Interface {
			res := bVal.Call([]reflect.Value{reflect.ValueOf(a)})
			return res[0].Bool()
		}
	}

	na := normalizeJSONValue(a)
	nb := normalizeJSONValue(b)
	return reflect.DeepEqual(na, nb) || fmt.Sprintf("%v", na) == fmt.Sprintf("%v", nb)
}
