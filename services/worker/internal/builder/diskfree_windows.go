//go:build windows

package builder

// FreeBytes is not measured on Windows (development only).
func FreeBytes(path string) (uint64, error) { return 1 << 62, nil }
