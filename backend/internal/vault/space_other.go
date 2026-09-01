//go:build !linux

package vault

import "errors"

// availableBytes is unsupported outside Linux; callers treat the error as
// "cannot tell" and proceed.
func availableBytes(string) (int64, error) {
	return 0, errors.New("vault: free space check is only implemented on linux")
}

// isMemoryBacked cannot be determined off Linux; callers treat the error as
// "cannot tell".
func isMemoryBacked(string) (bool, error) {
	return false, errors.New("vault: filesystem type check is only implemented on linux")
}
