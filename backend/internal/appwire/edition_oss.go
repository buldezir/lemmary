//go:build !lemmary_cloud && !lemmary_exttest

package appwire

import "lemmary/backend/internal/ext"

// edition returns what this build adds to the core feature set: nothing.
//
// The build tags above are negative on purpose. A private edition is a file
// carrying the matching positive tag, added to a fork of this repository — so
// the fork adds a file and never edits one, and pulling from upstream can
// therefore never conflict here. Any new edition tag has to be excluded from
// this file's constraint too, or the two definitions collide at compile time,
// which is the intended failure: two editions in one binary is not a build
// anybody meant to make.
func edition() ext.Edition { return ext.Edition{} }
