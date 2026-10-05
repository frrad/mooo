package sessionlogin

import "fmt"

// SGJSON is the narrow clean-room projection hook observed in SGJsonKit.
// Values implementing both SGJSON and SGNumberArray use SGJSON first.
type SGJSON interface {
	JSONObject() any
}

// SGNumberArray is the ordered-array projection hook observed in SGJsonKit.
type SGNumberArray interface {
	NumberArray() any
}

// SGJSONProperty is one class-property contribution. The source enumerator
// walks the dynamic class first, then each superclass until SGJsonObject; the
// caller must provide groups in that observed order. Property-list order
// within one class remains runtime-defined.
type SGJSONProperty struct {
	Name  string
	Value any
}

// SGJSONPropertySource supplies properties read from the receiver by KVC.
type SGJSONPropertySource interface {
	JSONProperties() []SGJSONProperty
}

// SGJSONNull represents Objective-C nil/NSNull in a projected dictionary.
// It marshals as JSON null while remaining distinguishable from a missing key.
type SGJSONNull struct{}

func (SGJSONNull) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// ProjectSGJSONObject applies the observed SGJsonObject conversion to one
// value. Protocol results are assigned directly; ordinary maps and slices are
// not recursively traversed because SGJsonKit only recurses through protocol
// implementations.
func ProjectSGJSONObject(input any) (any, error) {
	if input == nil {
		return SGJSONNull{}, nil
	}
	if object, ok := input.(SGJSON); ok {
		return object.JSONObject(), nil
	}
	if numbers, ok := input.(SGNumberArray); ok {
		return numbers.NumberArray(), nil
	}
	return input, nil
}

// ProjectSGJSONProperties projects property groups in source enumeration
// order. A nil property is retained as SGJSONNull rather than omitted.
func ProjectSGJSONProperties(properties ...[]SGJSONProperty) (map[string]any, error) {
	out := make(map[string]any)
	for _, group := range properties {
		for _, property := range group {
			if property.Name == "" {
				return nil, fmt.Errorf("sessionlogin: empty SGJSON property name")
			}
			value, err := ProjectSGJSONObject(property.Value)
			if err != nil {
				return nil, err
			}
			out[property.Name] = value
		}
	}
	return out, nil
}
