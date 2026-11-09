// Package app holds what this program is, as distinct from what it was told.
//
// The line against internal/config: whether an operator can change it without rebuilding.
package app

const Name = "mailio"

const ProjectURL = "https://github.com/reeywhaar/mailio"

// ListenAddr is the admin interface, fixed inside the container. Remap with `docker run -p`.
const ListenAddr = ":80"

// Version is stamped at link time:
//
//	-ldflags "-X mailio/internal/app.Version=$(git rev-parse --short HEAD)"
var Version = "dev"
