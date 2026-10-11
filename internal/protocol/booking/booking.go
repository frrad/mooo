// Package booking models the two plain-carriage bootstrap exchanges that
// precede LOGINLIST: GETCONF on the booking host, which names the checkin
// hosts and ports, and CHECKIN, which names the secure carriage endpoint. It
// does not dial or retry; internal/client owns the connection order.
package booking

import (
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	// Host is the booking server GETCONF is sent to.
	Host = "booking-loco.kakao.com"

	GetConfCommand = "GETCONF"
	CheckinCommand = "CHECKIN"

	// DefaultPort is used when GETCONF names no usable checkin port.
	DefaultPort = 995
)

var ErrMalformed = errors.New("booking: malformed reply")

// GetConfRequest is the GETCONF body.
type GetConfRequest struct {
	MCCMNC string
	Model  string
	OS     string
	UserID int64
}

func (r GetConfRequest) MarshalBSON() ([]byte, error) {
	return bson.Marshal(bson.D{
		{Key: "MCCMNC", Value: r.MCCMNC},
		{Key: "model", Value: r.Model},
		{Key: "os", Value: r.OS},
		{Key: "userId", Value: r.UserID},
	})
}

// CheckinRequest is the CHECKIN body.
type CheckinRequest struct {
	UserID     int64
	OS         string
	NType      int32
	AppVer     string
	MCCMNC     string
	Lang       string
	CountryISO string
	UseSub     bool
}

func (r CheckinRequest) MarshalBSON() ([]byte, error) {
	return bson.Marshal(bson.D{
		{Key: "userId", Value: r.UserID},
		{Key: "os", Value: r.OS},
		{Key: "ntype", Value: r.NType},
		{Key: "appVer", Value: r.AppVer},
		{Key: "MCCMNC", Value: r.MCCMNC},
		{Key: "lang", Value: r.Lang},
		{Key: "countryISO", Value: r.CountryISO},
		{Key: "useSub", Value: r.UseSub},
	})
}

// DecodeBookingTargets reads the checkin hosts (ticket.lsl) and ports
// (wifi.ports) from a GETCONF reply. Empty host strings and ports outside
// 1-65535 or not BSON int32 are skipped. At least one host is required; with
// no usable port the result is [DefaultPort].
func DecodeBookingTargets(body []byte) ([]string, []int, error) {
	raw := bson.Raw(body)
	ticket, err := raw.LookupErr("ticket")
	if err != nil || ticket.Type != bson.TypeEmbeddedDocument {
		return nil, nil, ErrMalformed
	}
	lsl, err := ticket.Document().LookupErr("lsl")
	if err != nil || lsl.Type != bson.TypeArray {
		return nil, nil, ErrMalformed
	}
	values, err := lsl.Array().Values()
	if err != nil {
		return nil, nil, ErrMalformed
	}
	var hosts []string
	for _, value := range values {
		if value.Type == bson.TypeString && value.StringValue() != "" {
			hosts = append(hosts, value.StringValue())
		}
	}
	wifi, err := raw.LookupErr("wifi")
	if err != nil || wifi.Type != bson.TypeEmbeddedDocument {
		return nil, nil, ErrMalformed
	}
	portValue, err := wifi.Document().LookupErr("ports")
	if err != nil || portValue.Type != bson.TypeArray {
		return nil, nil, ErrMalformed
	}
	values, err = portValue.Array().Values()
	if err != nil {
		return nil, nil, ErrMalformed
	}
	var ports []int
	for _, value := range values {
		if value.Type == bson.TypeInt32 {
			port := int(value.Int32())
			if port > 0 && port <= 65535 {
				ports = append(ports, port)
			}
		}
	}
	if len(hosts) == 0 {
		return nil, nil, ErrMalformed
	}
	if len(ports) == 0 {
		ports = []int{DefaultPort}
	}
	return hosts, ports, nil
}

// DefaultPingInterval is the foreground keepalive interval the Mac client
// uses when GETCONF supplies none or a value below one second.
const DefaultPingInterval = 180 * time.Second

// DecodeForegroundPingInterval reads wifi.fgPingItv, int32 seconds, from a
// GETCONF reply. Like the Mac client, it falls back to DefaultPingInterval
// for a value below 1. An absent or non-int32 value also falls back, matching
// the Mac model's zero default for a missing scalar.
func DecodeForegroundPingInterval(body []byte) time.Duration {
	wifi, err := bson.Raw(body).LookupErr("wifi")
	if err != nil || wifi.Type != bson.TypeEmbeddedDocument {
		return DefaultPingInterval
	}
	value, err := wifi.Document().LookupErr("fgPingItv")
	if err != nil || value.Type != bson.TypeInt32 || value.Int32() < 1 {
		return DefaultPingInterval
	}
	return time.Duration(value.Int32()) * time.Second
}

// DecodeCheckinEndpoint reads the secure carriage host and port from a CHECKIN
// reply. The port may be BSON int32 or int64 and must be in 1-65535.
func DecodeCheckinEndpoint(body []byte) (string, int, error) {
	raw := bson.Raw(body)
	hostValue, err := raw.LookupErr("host")
	if err != nil || hostValue.Type != bson.TypeString || hostValue.StringValue() == "" {
		return "", 0, ErrMalformed
	}
	portValue, err := raw.LookupErr("port")
	if err != nil {
		return "", 0, ErrMalformed
	}
	var port int
	switch portValue.Type {
	case bson.TypeInt32:
		port = int(portValue.Int32())
	case bson.TypeInt64:
		port = int(portValue.Int64())
	default:
		return "", 0, ErrMalformed
	}
	if port <= 0 || port > 65535 {
		return "", 0, ErrMalformed
	}
	return hostValue.StringValue(), port, nil
}
