//go:build !windows && !linux
// +build !windows,!linux

package governor

import (
	"errors"
	"time"
)

var ErrUnsupportedPlatform = errors.New("os resource provider is not supported on this platform")

// NewOSResourceProvider returns an error on unsupported platforms.
func NewOSResourceProvider(storagePaths []string, now func() time.Time) (OSResourceProvider, error) {
	return nil, ErrUnsupportedPlatform
}
