package media

import "github.com/frrad/mooo/internal/protocol/strictjson"

// Attachment JSON admission checks the root object only: unique keys, at most
// 64 of them, and no trailing data. The byte bounds are mooo policy.
var (
	attachmentJSON = strictjson.Limits{MaxBytes: 64 << 10, MaxKeys: 64}
	albumJSON      = strictjson.Limits{MaxBytes: 128 << 10, MaxKeys: 64}
	stickerJSON    = strictjson.Limits{MaxBytes: 16 << 10, MaxKeys: 64}
)
