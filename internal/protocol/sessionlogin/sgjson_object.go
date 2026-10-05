package sessionlogin

import (
	"encoding/json"
	"fmt"
)

// SGJSON is the narrow clean-room projection hook observed in SGJsonKit.
// Values implementing both SGJSON and SGNumberArray use SGJSON first.
type SGJSON interface {
	JSONObject() any
}

// SGNumberArray is the ordered-array projection hook observed in SGJsonKit.
type SGNumberArray interface {
	NumberArray() any
}

// SGJSONProperty is one dynamic-class property contribution. A caller may
// provide inherited properties first and dynamic-class properties afterward;
// later values replace an inherited key, matching KVC's final assignment.
type SGJSONProperty struct {
	Name  string
	Value any
}

// SGJSONPropertySource supplies the properties enumerated for a model class.
type SGJSONPropertySource interface {
	JSONProperties() []SGJSONProperty
}

// SGJSONNull represents Objective-C nil/NSNull in a projected dictionary.
// It marshals as JSON null while remaining distinguishable from a missing key.
type SGJSONNull struct{}

func (SGJSONNull) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// ProjectSGJSONObject applies the observed SGJsonObject.JSONObject value
// conversion recursively. It intentionally leaves ordinary scalar values
// unchanged and makes no claims about app-specific key mapping or wire order.
func ProjectSGJSONObject(input any) (any, error) {
	return projectSGJSONValue(input)
}

// ProjectSGJSONProperties projects inherited properties followed by dynamic
// properties. A nil property is retained as SGJSONNull rather than omitted.
func ProjectSGJSONProperties(properties ...[]SGJSONProperty) (map[string]any, error) {
	out := make(map[string]any)
	for _, group := range properties {
		for _, property := range group {
			if property.Name == "" {
				return nil, fmt.Errorf("sessionlogin: empty SGJSON property name")
			}
			value, err := projectSGJSONValue(property.Value)
			if err != nil {
				return nil, err
			}
			out[property.Name] = value
		}
	}
	return out, nil
}

func projectSGJSONValue(input any) (any, error) {
	if input == nil {
		return SGJSONNull{}, nil
	}
	if source, ok := input.(SGJSONPropertySource); ok {
		properties := source.JSONProperties()
		return ProjectSGJSONProperties(properties)
	}
	if object, ok := input.(SGJSON); ok {
		return projectSGJSONValue(object.JSONObject())
	}
	if numbers, ok := input.(SGNumberArray); ok {
		return projectSGJSONValue(numbers.NumberArray())
	}
	switch value := input.(type) {
	case SGJSONNull:
		return value, nil
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			projected, err := projectSGJSONValue(item)
			if err != nil {
				return nil, err
			}
			out[key] = projected
		}
		return out, nil
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			projected, err := projectSGJSONValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = projected
		}
		return out, nil
	case json.Number, string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return value, nil
	default:
		return value, nil
	}
}
