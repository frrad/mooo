package sessionlogin

// ReceiptJSONMapping names a source and destination key in the observed
// receipt JSON mapping phase. Mapping entries are applied in slice order.
type ReceiptJSONMapping struct {
	Destination string
	Source      string
}

// MapReceiptJSONObject applies the bounded receipt mapping phase to a
// superclass JSON result. Non-dictionary results are returned unchanged. A
// dictionary is shallow-copied, static keys are removed, and each present
// non-null source is assigned to its destination before the source is removed.
// Values remain opaque and retain their original identity.
func MapReceiptJSONObject(superResult any, removeKeys []string, mappings []ReceiptJSONMapping) any {
	input, ok := superResult.(map[string]any)
	if !ok {
		return superResult
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	for _, key := range removeKeys {
		delete(out, key)
	}
	for _, mapping := range mappings {
		value, present := out[mapping.Source]
		if !present {
			continue
		}
		if !isReceiptJSONNull(value) {
			out[mapping.Destination] = value
		}
		delete(out, mapping.Source)
	}
	return out
}

func isReceiptJSONNull(value any) bool {
	if value == nil {
		return true
	}
	_, ok := value.(SGJSONNull)
	return ok
}
