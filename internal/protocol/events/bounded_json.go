package events

import "encoding/json"

// Validate all nested objects before unmarshalling so duplicate discriminator,
// title or option fields cannot change meaning between consumers.
func validateBoundedJSON(dec *json.Decoder, depth int, budget *int) error {
	*budget -= 1
	if depth > 16 || *budget < 0 {
		return ErrMalformedEvent
	}
	tok, err := dec.Token()
	if err != nil {
		return ErrMalformedEvent
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			tok, err = dec.Token()
			key, ok := tok.(string)
			if err != nil || !ok || seen[key] || len(seen) >= 64 {
				return ErrMalformedEvent
			}
			seen[key] = true
			if err = validateBoundedJSON(dec, depth+1, budget); err != nil {
				return err
			}
		}
		tok, err = dec.Token()
		if err != nil || tok != json.Delim('}') {
			return ErrMalformedEvent
		}
	case '[':
		count := 0
		for dec.More() {
			count++
			if count > 128 {
				return ErrMalformedEvent
			}
			if err = validateBoundedJSON(dec, depth+1, budget); err != nil {
				return err
			}
		}
		tok, err = dec.Token()
		if err != nil || tok != json.Delim(']') {
			return ErrMalformedEvent
		}
	default:
		return ErrMalformedEvent
	}
	return nil
}
