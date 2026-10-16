//go:build !darwin

package connector

// Hosts other than macOS have no Computer Name or hw.model.
func hostComputerName() (string, bool) { return "", false }

func hostHardwareModel() (string, bool) { return "", false }
