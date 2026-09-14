// Package app holds what mailio is, as distinct from what it was told.
//
// The line between this package and internal/config is whether an operator can
// change it without rebuilding. They can edit every domain and account in
// config.yml; they cannot make this program a different project.
package app

// Name is what this program is called, on the command line and in its output.
const Name = "mailio"

// ProjectURL is where somebody wondering what mailio is can go and read it.
const ProjectURL = "https://github.com/reeywhaar/mailio"

// Version is the build this is, stamped at link time:
//
//	-ldflags "-X mailio/internal/app.Version=$(git rev-parse --short HEAD)"
//
// "dev" is what a local build says, and it is true.
var Version = "dev"
