// Package strictjson is the bounded JSON admission check shared by the
// protocol decoders. It rejects input that a lenient decoder would accept
// ambiguously: duplicate object keys (whose last-wins resolution can differ
// between consumers), trailing data after the value, and input past the
// caller's size, key, depth and element bounds. These are mooo admission
// rules, not claims about how the official client treats malformed input.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var (
	// ErrTooLarge reports input longer than Limits.MaxBytes.
	ErrTooLarge = errors.New("strictjson: input too large")
	// ErrInvalid covers every other rejection, including a json.Unmarshal
	// failure in Unmarshal.
	ErrInvalid = errors.New("strictjson: invalid JSON")
)

// Limits bounds one input. A zero field imposes no limit.
type Limits struct {
	// MaxBytes bounds the whole input.
	MaxBytes int
	// MaxKeys bounds the keys of each checked object.
	MaxKeys int
	// Nested extends the duplicate-key, MaxKeys and MaxArray checks to every
	// nested container and enables MaxDepth and MaxValues. Without it only
	// the root container is checked and nested values need only be valid
	// JSON.
	Nested bool
	// MaxDepth bounds how many containers deep below the root a value may
	// sit; the root is depth 0.
	MaxDepth int
	// MaxArray bounds the elements of each checked array.
	MaxArray int
	// MaxValues bounds the total number of values, counting containers and
	// scalars but not object keys.
	MaxValues int
}

// Validate reports whether data is exactly one JSON value, optionally
// surrounded by whitespace, that satisfies limits. The root may be any JSON
// value; use Unmarshal when the root must be an object.
func Validate(data []byte, limits Limits) error {
	if limits.MaxBytes > 0 && len(data) > limits.MaxBytes {
		return ErrTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	w := walker{dec: dec, limits: limits}
	if err := w.value(0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

// Unmarshal checks that data is one JSON object satisfying limits and then
// decodes it into v with json.Unmarshal.
func Unmarshal(data []byte, v any, limits Limits) error {
	if limits.MaxBytes > 0 && len(data) > limits.MaxBytes {
		return ErrTooLarge
	}
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ErrInvalid
	}
	if err := Validate(data, limits); err != nil {
		return err
	}
	if json.Unmarshal(data, v) != nil {
		return ErrInvalid
	}
	return nil
}

type walker struct {
	dec    *json.Decoder
	limits Limits
	values int
}

func (w *walker) value(depth int) error {
	if depth > 0 && !w.limits.Nested {
		var raw json.RawMessage
		if w.dec.Decode(&raw) != nil {
			return ErrInvalid
		}
		return nil
	}
	if w.limits.Nested {
		w.values++
		if w.limits.MaxDepth > 0 && depth > w.limits.MaxDepth ||
			w.limits.MaxValues > 0 && w.values > w.limits.MaxValues {
			return ErrInvalid
		}
	}
	tok, err := w.dec.Token()
	if err != nil {
		return ErrInvalid
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for w.dec.More() {
			tok, err = w.dec.Token()
			key, ok := tok.(string)
			if err != nil || !ok || seen[key] || w.limits.MaxKeys > 0 && len(seen) >= w.limits.MaxKeys {
				return ErrInvalid
			}
			seen[key] = true
			if err = w.value(depth + 1); err != nil {
				return err
			}
		}
	case '[':
		count := 0
		for w.dec.More() {
			count++
			if w.limits.MaxArray > 0 && count > w.limits.MaxArray {
				return ErrInvalid
			}
			if err = w.value(depth + 1); err != nil {
				return err
			}
		}
	default:
		return ErrInvalid
	}
	// More reported false, so the next token is the matching close delimiter
	// or a syntax error.
	if _, err = w.dec.Token(); err != nil {
		return ErrInvalid
	}
	return nil
}
