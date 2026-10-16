package connector

import "context"

// qrDeviceName returns the name a new QR device registers under. A configured
// name is sent unchanged and the host is not read. Otherwise it follows the
// official Mac client (research/device-registration/DEVICE-NAME.md): the macOS
// Computer Name as-is, then the hardware model identifier, then "". Hosts
// without these sources, such as Linux, therefore send "".
func qrDeviceName(ctx context.Context, configured string, computerName, hardwareModel func(context.Context) (string, bool)) string {
	if configured != "" {
		return configured
	}
	if name, ok := computerName(ctx); ok {
		return name
	}
	if model, ok := hardwareModel(ctx); ok {
		return model
	}
	return ""
}
