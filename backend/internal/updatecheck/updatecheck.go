// Package updatecheck tells an admin when a newer image than the running one
// has been published. The image says which one it is through LEMMARY_VERSION
// and LEMMARY_REVISION, which CI bakes in (see Dockerfile): a release image
// (version "v2026.10.13") looks for a newer GitHub release, a main image
// (version "main") for a `latest` built from another commit. Anything else --
// a local build, `go run`, a pull request image -- is not checked.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

const (
	EnvVersion  = "LEMMARY_VERSION"
	EnvRevision = "LEMMARY_REVISION"

	repo     = "buldezir/lemmary"
	cronExpr = "17 */12 * * *"

	revisionLabel = "org.opencontainers.image.revision"
)

var (
	githubAPI   = "https://api.github.com"
	githubWeb   = "https://github.com"
	registryURL = "https://ghcr.io"
	client      = &http.Client{Timeout: 30 * time.Second}
)

// Status is what the header shows. Version and URL are set only for a release.
type Status struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	URL       string `json:"url,omitempty"`
}

var current atomic.Pointer[Status]

// Current is the last successful check's answer; nothing available before one.
func Current() Status {
	if s := current.Load(); s != nil {
		return *s
	}
	return Status{}
}

// Register checks once the server is up and then twice a day.
func Register(app core.App, version, revision string) {
	check := checkFor(version, revision)
	if check == nil {
		return
	}
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		s, err := check(ctx)
		if err != nil {
			app.Logger().Warn("update check failed", slog.Any("error", err))
			return
		}
		current.Store(&s)
	}
	app.Cron().MustAdd("update_check", cronExpr, run)
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		go run()
		return e.Next()
	})
}

func checkFor(version, revision string) func(context.Context) (Status, error) {
	switch {
	case parseRelease(version) != nil:
		return func(ctx context.Context) (Status, error) { return checkRelease(ctx, version) }
	case version == "main" && revision != "":
		return func(ctx context.Context) (Status, error) { return checkLatest(ctx, revision) }
	}
	return nil
}

func checkRelease(ctx context.Context, running string) (Status, error) {
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := getJSON(ctx, githubAPI+"/repos/"+repo+"/releases/latest", nil, &rel); err != nil {
		return Status{}, err
	}
	latest := parseRelease(rel.TagName)
	if latest == nil {
		return Status{}, fmt.Errorf("latest release %q is not a version", rel.TagName)
	}
	if slices.Compare(latest, parseRelease(running)) <= 0 {
		return Status{}, nil
	}
	return Status{
		Available: true,
		Version:   rel.TagName,
		URL:       githubWeb + "/" + repo + "/releases/tag/" + url.PathEscape(rel.TagName),
	}, nil
}

// checkLatest reads the revision label off `latest`'s config for this
// architecture. `latest` only ever moves forward along main, so any revision
// other than ours is newer.
func checkLatest(ctx context.Context, revision string) (Status, error) {
	var token struct {
		Token string `json:"token"`
	}
	if err := getJSON(ctx, registryURL+"/token?scope=repository:"+repo+":pull", nil, &token); err != nil {
		return Status{}, err
	}
	base := registryURL + "/v2/" + repo
	header := http.Header{"Authorization": {"Bearer " + token.Token}}

	header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json")
	var index struct {
		Manifests []platformManifest `json:"manifests"`
	}
	if err := getJSON(ctx, base+"/manifests/latest", header, &index); err != nil {
		return Status{}, err
	}
	i := slices.IndexFunc(index.Manifests, func(m platformManifest) bool {
		return m.Platform.OS == "linux" && m.Platform.Architecture == runtime.GOARCH
	})
	if i < 0 {
		return Status{}, fmt.Errorf("latest has no linux/%s image", runtime.GOARCH)
	}

	header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	var manifest struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	if err := getJSON(ctx, base+"/manifests/"+index.Manifests[i].Digest, header, &manifest); err != nil {
		return Status{}, err
	}

	header.Del("Accept")
	var config struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if err := getJSON(ctx, base+"/blobs/"+manifest.Config.Digest, header, &config); err != nil {
		return Status{}, err
	}
	latest := config.Config.Labels[revisionLabel]
	if latest == "" {
		return Status{}, fmt.Errorf("latest carries no %s label", revisionLabel)
	}
	return Status{Available: latest != revision}, nil
}

type platformManifest struct {
	Digest   string `json:"digest"`
	Platform struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}

// parseRelease is nil for anything that is not "v" and dot-separated numbers.
func parseRelease(v string) []int {
	rest, ok := strings.CutPrefix(v, "v")
	if !ok {
		return nil
	}
	var parts []int
	for p := range strings.SplitSeq(rest, ".") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return nil
		}
		parts = append(parts, n)
	}
	return parts
}

func getJSON(ctx context.Context, target string, header http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	maps.Copy(req.Header, header)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", req.URL.Redacted(), resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
