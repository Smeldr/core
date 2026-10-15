package smeldr

import "encoding/json"

// asItem returns an MCPCreate or MCPUpdate result as T: the typed item, or the
// item inside a result that carries warnings (D110, D112).
func asItem[T any](v any) T {
	if t, ok := v.(T); ok {
		return t
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var t T
	if err := json.Unmarshal(b, &t); err != nil {
		panic(err)
	}
	return t
}
