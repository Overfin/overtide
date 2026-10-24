// Package product contains the small set of identity values that must change
// together when the application is renamed or released.
package product

const (
	Name = "Overtide"
	Slug = "overtide"
)

// Version is set when building, from the git tag: go build -ldflags
// "-X github.com/overfin/overtide/internal/product.Version=v0.6.0".
var Version = "dev"
