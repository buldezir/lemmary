package appwire

import (
	"io/fs"
	"path"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

const (
	immutableCacheControl  = "public, max-age=31536000, immutable"
	revalidateCacheControl = "no-cache"
)

// Vite and VitePress content-hash every file they emit into these directories.
var hashedAssetDirs = []string{"assets/", "docs/assets/"}

func staticCacheControl(fsys fs.FS, filename string) string {
	name := strings.TrimPrefix(path.Clean("/"+filename), "/")
	for _, dir := range hashedAssetDirs {
		if !strings.HasPrefix(name, dir) {
			continue
		}
		// A missing file under these prefixes is answered by the index.html
		// fallback, which must not inherit the asset's immutable lifetime.
		if _, err := fs.Stat(fsys, name); err == nil {
			return immutableCacheControl
		}
	}
	return revalidateCacheControl
}

func staticWithCacheControl(fsys fs.FS, indexFallback bool) func(*core.RequestEvent) error {
	static := apis.Static(fsys, indexFallback)
	return func(e *core.RequestEvent) error {
		e.Response.Header().Set(
			"Cache-Control",
			staticCacheControl(fsys, e.Request.PathValue(apis.StaticWildcardParam)),
		)
		return static(e)
	}
}
