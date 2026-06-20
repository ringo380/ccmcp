package discovery

import _ "embed"

// registryBytes holds the curated, ccmcp-maintained registry that ships with
// every binary. New seed entries are added by editing registry.json and
// landing a PR - there is no runtime mutation path.
//
//go:embed registry.json
var registryBytes []byte

// EmbeddedRegistry returns the raw bytes of the curated registry - exposed for
// tests and for the `ccmcp discover sources` debug surface.
func EmbeddedRegistry() []byte {
	out := make([]byte, len(registryBytes))
	copy(out, registryBytes)
	return out
}
