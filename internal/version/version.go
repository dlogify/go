// Package version holds the module version, shared by the root package and
// the internal packages (which cannot import the root package).
package version

// Version is the module version; scripts/release.sh checks it against the tag.
const Version = "0.1.0"

const (
	UserAgent   = "logify-go/" + Version
	SDKName     = "logify.go"
	SDKLanguage = "go"
)
