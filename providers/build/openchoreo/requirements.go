package openchoreo

import "github.com/serendip-infra/serendip-infra/core/provider"

// Requirements is this package's measured cost (specs/022-builds plan
// decision 4), which the package catalogue points at. It stays nil until
// T013 measures it on the simulated T470, and until then the package
// can't be installed: Catalogue.Validate refuses it.
var Requirements *provider.Requirements
