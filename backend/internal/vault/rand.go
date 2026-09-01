package vault

import "crypto/rand"

// randRead fills b with cryptographically secure random bytes. It exists as a
// seam so tests can assert that nonces are actually random rather than a stub.
func randRead(b []byte) error {
	_, err := rand.Read(b)
	return err
}
