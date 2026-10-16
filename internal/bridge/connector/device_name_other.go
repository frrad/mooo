//go:build !darwin

package connector

import "context"

// Hosts other than macOS have no Computer Name or hw.model.
func hostComputerName(context.Context) (string, bool) { return "", false }

func hostHardwareModel(context.Context) (string, bool) { return "", false }
