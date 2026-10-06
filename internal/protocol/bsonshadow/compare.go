// Package bsonshadow compares how the production BSON decoder
// (mongo-driver) and the source-compatible observed decoder
// (loco.DecodeObservedBSON) read the same LOCO body. Production callers keep
// using mongo-driver; this package only reports where the official client
// would have seen different data.
//
// Reports are safe to log: they carry field paths, discrepancy kinds and
// value descriptors (type and length), never values. Keys that do not look
// like protocol field names are replaced by a short hash.
package bsonshadow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
)

// Kind classifies a discrepancy. Every kind is a difference in the data a
// caller would observe; there is no "harmless" kind.
type Kind string

const (
	// KindMongoRejects: mongo-driver fails to decode a body the official
	// decoder accepts, so mooo callers error where the official client reads.
	KindMongoRejects Kind = "mongo-rejects"
	// KindOfficialRejects: the official decoder fails where mongo-driver
	// accepts, so mooo acts on a body the official client discards.
	KindOfficialRejects Kind = "official-rejects"
	// KindShadowBounds: the observed decoder hit a Go safety bound. This is
	// a limit of the shadow, not proof of official behavior.
	KindShadowBounds Kind = "shadow-bounds"
	// KindOfficialPartial: the official decoder stopped at an unknown element
	// type and returned only the fields before it.
	KindOfficialPartial Kind = "official-partial"
	// KindOfficialDroppedInvalidUTF8: mongo-driver keeps a string with
	// invalid UTF-8; the official decoder drops the value.
	KindOfficialDroppedInvalidUTF8 Kind = "official-dropped-invalid-utf8"
	// KindOfficialIgnoresType: mongo-driver has a value of a type the
	// official decoder recognizes but never assigns.
	KindOfficialIgnoresType Kind = "official-ignores-type"
	// KindMongoOnly: mongo-driver has a field the official result lacks for
	// another reason (for example after a partial decode).
	KindMongoOnly Kind = "mongo-only"
	// KindOfficialOnly: the official result has a field mongo-driver lacks.
	KindOfficialOnly Kind = "official-only"
	// KindNullOverwrites: a later null resets the field for mongo-driver
	// struct decoding while the official decoder keeps the earlier value.
	KindNullOverwrites Kind = "null-overwrites"
	// KindStringNulTruncated: the official decoder ends the string at its
	// first NUL; mongo-driver keeps the declared length.
	KindStringNulTruncated Kind = "string-nul-truncated"
	// KindTypeDiffers: both decoders have the field with different types.
	KindTypeDiffers Kind = "type-differs"
	// KindValueDiffers: same type, different value.
	KindValueDiffers Kind = "value-differs"
	// KindArrayLength: arrays have different lengths (for example the
	// official decoder skips null elements).
	KindArrayLength Kind = "array-length"
)

// Discrepancy is one log-safe difference. Path is "" for the document root.
type Discrepancy struct {
	Path     string `json:"path"`
	Kind     Kind   `json:"kind"`
	Mongo    string `json:"mongo"`
	Official string `json:"official"`
}

// Report is the result of comparing one body. Errors are kept for the caller
// but may contain decoder detail; log Discrepancies, not the errors.
type Report struct {
	Discrepancies []Discrepancy
	MongoErr      error
	OfficialErr   error
}

// Compare decodes body with both decoders and reports every difference.
func Compare(body []byte) Report {
	var mongoDoc bson.D
	mongoErr := bson.Unmarshal(body, &mongoDoc)
	official, officialErr := loco.DecodeObservedBSON(body, loco.BSONDecodeOptions{})
	report := Report{MongoErr: mongoErr, OfficialErr: officialErr}
	switch {
	case mongoErr != nil && officialErr != nil:
		return report
	case mongoErr != nil:
		report.add("", KindMongoRejects, "error", "document")
		return report
	case officialErr != nil:
		kind := KindOfficialRejects
		if errors.Is(officialErr, loco.ErrBSONDecodeBounds) {
			kind = KindShadowBounds
		}
		report.add("", kind, "document", "error")
		return report
	}
	if official.Partial {
		report.add("", KindOfficialPartial, "document", fmt.Sprintf("unknown-type(0x%02x)", official.UnknownType))
	}
	report.compareDocument("", mongoDoc, official.Document)
	return report
}

func (r *Report) add(path string, kind Kind, mongo, official string) {
	r.Discrepancies = append(r.Discrepancies, Discrepancy{Path: path, Kind: kind, Mongo: mongo, Official: official})
}

// collapse applies mongo-driver struct-decoding semantics to an ordered
// document: a later duplicate key replaces an earlier one, including a null.
func collapse(doc bson.D) map[string]any {
	out := make(map[string]any, len(doc))
	for _, element := range doc {
		out[element.Key] = element.Value
	}
	return out
}

func (r *Report) compareDocument(path string, mongoDoc bson.D, official map[string]any) {
	mongo := collapse(mongoDoc)
	keys := make([]string, 0, len(mongo)+len(official))
	for key := range mongo {
		keys = append(keys, key)
	}
	for key := range official {
		if _, ok := mongo[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		child := joinPath(path, key)
		mongoValue, inMongo := mongo[key]
		officialValue, inOfficial := official[key]
		switch {
		case inMongo && !inOfficial:
			r.compareMissingOfficial(child, mongoValue)
		case !inMongo && inOfficial:
			r.add(child, KindOfficialOnly, "absent", describe(officialValue))
		default:
			r.compareValue(child, mongoValue, officialValue)
		}
	}
}

func (r *Report) compareMissingOfficial(path string, mongoValue any) {
	switch value := mongoValue.(type) {
	case nil, bson.Undefined:
		// A struct field decoded from null keeps its zero value, which is the
		// same observable state as an absent key.
		return
	case string:
		if !utf8.ValidString(beforeNul(value)) {
			r.add(path, KindOfficialDroppedInvalidUTF8, describe(value), "absent")
			return
		}
	default:
		if officialIgnoresType(mongoValue) {
			r.add(path, KindOfficialIgnoresType, describe(mongoValue), "absent")
			return
		}
	}
	r.add(path, KindMongoOnly, describe(mongoValue), "absent")
}

func (r *Report) compareValue(path string, mongoValue, officialValue any) {
	switch mongo := mongoValue.(type) {
	case nil, bson.Undefined:
		r.add(path, KindNullOverwrites, describe(mongoValue), describe(officialValue))
	case string:
		official, ok := officialValue.(string)
		switch {
		case !ok:
			r.add(path, KindTypeDiffers, describe(mongoValue), describe(officialValue))
		case mongo == official:
		case beforeNul(mongo) == official && strings.IndexByte(mongo, 0) >= 0:
			r.add(path, KindStringNulTruncated, describe(mongoValue), describe(officialValue))
		default:
			r.add(path, KindValueDiffers, describe(mongoValue), describe(officialValue))
		}
	case int32, int64, bool:
		if fmt.Sprintf("%T", mongoValue) != fmt.Sprintf("%T", officialValue) {
			r.add(path, KindTypeDiffers, describe(mongoValue), describe(officialValue))
		} else if mongoValue != officialValue {
			r.add(path, KindValueDiffers, describe(mongoValue), describe(officialValue))
		}
	case float64:
		official, ok := officialValue.(float64)
		if !ok {
			r.add(path, KindTypeDiffers, describe(mongoValue), describe(officialValue))
		} else if math.Float64bits(mongo) != math.Float64bits(official) && !(math.IsNaN(mongo) && math.IsNaN(official)) {
			r.add(path, KindValueDiffers, describe(mongoValue), describe(officialValue))
		}
	case bson.D:
		official, ok := officialValue.(map[string]any)
		if !ok {
			r.add(path, KindTypeDiffers, describe(mongoValue), describe(officialValue))
			return
		}
		r.compareDocument(path, mongo, official)
	case bson.A:
		official, ok := officialValue.([]any)
		if !ok {
			r.add(path, KindTypeDiffers, describe(mongoValue), describe(officialValue))
			return
		}
		if len(mongo) != len(official) {
			r.add(path, KindArrayLength, describe(mongoValue), describe(officialValue))
		}
		for i := 0; i < len(mongo) && i < len(official); i++ {
			r.compareValue(path+"["+strconv.Itoa(i)+"]", mongo[i], official[i])
		}
	default:
		// Only types the official decoder never assigns reach here, so the
		// official value necessarily came from an earlier duplicate.
		r.add(path, KindTypeDiffers, describe(mongoValue), describe(officialValue))
	}
}

func officialIgnoresType(value any) bool {
	switch value.(type) {
	case bson.Binary, bson.ObjectID, bson.DateTime, bson.Regex, bson.DBPointer,
		bson.JavaScript, bson.Symbol, bson.CodeWithScope, bson.Timestamp:
		return true
	}
	return false
}

func beforeNul(value string) string {
	if i := strings.IndexByte(value, 0); i >= 0 {
		return value[:i]
	}
	return value
}

// describe returns a value-free descriptor: type plus size where useful.
func describe(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bson.Undefined:
		return "undefined"
	case string:
		return fmt.Sprintf("string(len=%d)", len(v))
	case int32:
		return "int32"
	case int64:
		return "int64"
	case float64:
		return "double"
	case bool:
		return "bool"
	case bson.D:
		return fmt.Sprintf("document(n=%d)", len(v))
	case map[string]any:
		return fmt.Sprintf("document(n=%d)", len(v))
	case bson.A:
		return fmt.Sprintf("array(n=%d)", len(v))
	case []any:
		return fmt.Sprintf("array(n=%d)", len(v))
	case bson.Binary:
		return fmt.Sprintf("binary(len=%d)", len(v.Data))
	case bson.ObjectID:
		return "objectid"
	case bson.DateTime:
		return "datetime"
	case bson.Regex:
		return "regex"
	case bson.DBPointer:
		return "dbpointer"
	case bson.JavaScript:
		return "javascript"
	case bson.Symbol:
		return "symbol"
	case bson.CodeWithScope:
		return "code-with-scope"
	case bson.Timestamp:
		return "timestamp"
	case bson.Decimal128:
		return "decimal128"
	case bson.MinKey:
		return "minkey"
	case bson.MaxKey:
		return "maxkey"
	default:
		return fmt.Sprintf("%T", value)
	}
}

var safeKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// joinPath appends key to path. Keys that do not look like protocol field
// names (numeric IDs, free text, invalid UTF-8) are replaced by a short hash
// so paths stay log-safe while remaining stable for correlation.
func joinPath(path, key string) string {
	if !safeKey.MatchString(key) {
		sum := sha256.Sum256([]byte(key))
		key = "<key:" + hex.EncodeToString(sum[:4]) + ">"
	}
	if path == "" {
		return key
	}
	return path + "." + key
}
