package vault

import "crypto/rand"

// randRead is a seam so tests can assert that nonces are actually random.
func randRead(b []byte) error {
	_, err := rand.Read(b)
	return err
}
