package sessionlogin

import (
	"errors"
	"fmt"
	"reflect"
)

var ErrSGJSONNilProtocolResult = errors.New("sessionlogin: SGJson protocol returned nil")

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
		if isNilReference(input) {
			return SGJSONNull{}, nil
		}
		value := object.JSONObject()
		if isNilReference(value) {
			return nil, ErrSGJSONNilProtocolResult
		}
		return value, nil
	}
	if numbers, ok := input.(SGNumberArray); ok {
		if isNilReference(input) {
			return SGJSONNull{}, nil
		}
		value := numbers.NumberArray()
		if isNilReference(value) {
			return nil, ErrSGJSONNilProtocolResult
		}
		return value, nil
	}
	return input, nil
}

func isNilReference(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
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

// ProjectSGJSONNamedProperties models the NSObject category's class-property
// walk. Names are the runtime-enumerated dynamic-class and superclass keys;
// values are read from the same receiver by KVC. Undeclared values are never
// visited, and a declared getter returning nil becomes SGJSONNull.
func ProjectSGJSONNamedProperties(names []string, values map[string]any) (map[string]any, error) {
	properties := make([]SGJSONProperty, 0, len(names))
	for _, name := range names {
		properties = append(properties, SGJSONProperty{Name: name, Value: values[name]})
	}
	return ProjectSGJSONProperties(properties)
}
