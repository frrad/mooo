package sessionlogin

import "math"

// coerceFoundationTi reproduces the bounded Foundation KVC conversion used by
// incoming Ti (signed int32) properties for decoder-produced scalar values.
// It is intentionally limited to the scalar domains currently traced from the
// official decoder; callers retain responsibility for NSNull and other objects.
func coerceFoundationTi(value any) (int32, bool) {
	switch v := value.(type) {
	case int32:
		return v, true
	case int64:
		return int32(uint32(v)), true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	case float64:
		if math.IsNaN(v) {
			return 0, true
		}
		const maxInt64AsFloat = 9223372036854775808.0
		if math.IsInf(v, 1) || v >= maxInt64AsFloat {
			return -1, true
		}
		if math.IsInf(v, -1) || v <= -maxInt64AsFloat {
			return 0, true
		}
		return int32(uint32(int64(math.Trunc(v)))), true
	default:
		return 0, false
	}
}
