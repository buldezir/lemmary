package appwire

import (
	"testing"
	"testing/fstest"
)

func TestStaticCacheControl(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":                  {},
		"assets/index-BDxShJXC.js":    {},
		"docs/index.html":             {},
		"docs/assets/app.C-UgN_Im.js": {},
	}

	cases := map[string]string{
		"":                            revalidateCacheControl,
		"index.html":                  revalidateCacheControl,
		"documents/42":                revalidateCacheControl,
		"docs/index.html":             revalidateCacheControl,
		"assets/index-BDxShJXC.js":    immutableCacheControl,
		"docs/assets/app.C-UgN_Im.js": immutableCacheControl,
		// SPA fallback territory: no file, so no immutable lifetime.
		"assets/gone.js": revalidateCacheControl,
	}

	for filename, want := range cases {
		if got := staticCacheControl(fsys, filename); got != want {
			t.Errorf("staticCacheControl(%q) = %q, want %q", filename, got, want)
		}
	}
}
