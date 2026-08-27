//go:build !lemmary_cloud && !lemmary_exttest

package boot

// current returns what this build does before the app is constructed: nothing.
//
// The build tags above are negative on purpose, mirroring
// appwire/edition_oss.go: a private build is a file carrying the matching
// positive tag, added to a fork of this repository — so the fork adds a file
// and never edits one, and pulling from upstream can therefore never conflict
// here. Any new build tag has to be excluded from this file's constraint too,
// or the two definitions collide at compile time, which is the intended
// failure: two of these in one binary is not a build anybody meant to make.
func current() Boot { return Boot{} }
