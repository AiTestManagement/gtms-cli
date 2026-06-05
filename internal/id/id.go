// Package id generates unique 8-character hexadecimal identifiers
// for GTMS task files using crypto/rand.
package id

import (
	"crypto/rand"
	"fmt"
)

// New returns an 8-character lowercase hexadecimal string generated
// from crypto/rand. It panics if the system's cryptographic random
// number generator fails.
func New() string {
	b := make([]byte, 4) // 4 bytes = 8 hex chars
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return fmt.Sprintf("%x", b)
}
