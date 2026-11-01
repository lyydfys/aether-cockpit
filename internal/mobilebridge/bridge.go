// Package mobilebridge owns the narrow string ABI version and buffer helper.
// The process-wide runtime lives in internal/mobile.
package mobilebridge

// ABIVersion identifies the exported c-shared contract. Callers must reject an
// unknown version instead of guessing the contract.
const ABIVersion = "mobilebridge/2"

// WriteABIVersion writes ABIVersion and a trailing NUL into dst and returns the
// number of bytes written. When dst is too small it writes nothing and returns
// the negated required size, so callers can retry with a larger buffer.
func WriteABIVersion(dst []byte) int {
	needed := len(ABIVersion) + 1
	if len(dst) < needed {
		return -needed
	}
	copy(dst, ABIVersion)
	dst[len(ABIVersion)] = 0
	return needed
}
