package events

import "github.com/frrad/mooo/internal/protocol/strictjson"

// boundedEventJSON validates every nested object before unmarshalling so
// duplicate discriminator, title or option fields cannot change meaning
// between consumers.
var boundedEventJSON = strictjson.Limits{MaxBytes: 64 << 10, MaxKeys: 64, Nested: true, MaxDepth: 16, MaxArray: 128, MaxValues: 1024}

// attachmentJSON checks only the root object of a single-object attachment.
var attachmentJSON = strictjson.Limits{MaxBytes: 64 << 10, MaxKeys: 64}

func validBoundedEventJSON(value string) bool {
	return validEventString(value) && strictjson.Validate([]byte(value), boundedEventJSON) == nil
}
