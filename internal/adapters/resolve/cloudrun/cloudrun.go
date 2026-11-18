package cloudrun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

const (
	defaultRunEndpoint      = "https://run.googleapis.com/v2/"
	defaultRegistryEndpoint = "https://artifactregistry.googleapis.com/v1/"
	registryHostSuffix      = "-docker.pkg.dev"
)

var commitTag = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Resolves a service revision or job execution to the commit its image was
// built from, via the image's short-SHA tag in Artifact Registry. Neither
// changes image once created, so answers are cached for the life of the process.
type Resolver struct {
	Client *http.Client
	// The repository the repo's code is built into, e.g.
	// us-central1-docker.pkg.dev/proj/repo/app. Workloads running any other
	// image get pipeline.ErrOtherCode. Empty accepts every image.
	Image            string
	RunEndpoint      string
	RegistryEndpoint string

	cache     map[string]string
	otherCode map[string]bool
	tags      map[string]string
}

var _ pipeline.Resolver = (*Resolver)(nil)

type image struct {
	location, project, repository, pkg, digest string
}

func (r *Resolver) SHA(ctx context.Context, e event.Event) (string, error) {
	if e.Project == "" || e.Location == "" || e.Service == "" || e.Revision == "" {
		return "", fmt.Errorf("cloudrun: event lacks project, location, service or revision")
	}
	key := path.Join(e.Project, e.Location, workload(e), e.Service, e.Revision)
	if sha, ok := r.cache[key]; ok {
		return sha, nil
	}
	if r.otherCode[key] {
		return "", pipeline.ErrOtherCode
	}

	refs, err := r.images(ctx, e)
	if err != nil {
		return "", err
	}
	ref, ok := r.ours(refs)
	if !ok {
		r.otherCode = setKey(r.otherCode, key)
		return "", fmt.Errorf("cloudrun: %s %s runs %v: %w", workload(e), e.Revision, refs, pipeline.ErrOtherCode)
	}
	sha, err := r.commitTagCached(ctx, ref)
	if err != nil {
		return "", err
	}

	if r.cache == nil {
		r.cache = map[string]string{}
	}
	r.cache[key] = sha
	return sha, nil
}

func workload(e event.Event) string {
	if e.Job {
		return "job"
	}
	return "service"
}

// A revision reports the digests it actually pulled; an execution's template
// already has its tag resolved to a digest.
func (r *Resolver) images(ctx context.Context, e event.Event) ([]string, error) {
	base := or(r.RunEndpoint, defaultRunEndpoint) + fmt.Sprintf("projects/%s/locations/%s/",
		url.PathEscape(e.Project), url.PathEscape(e.Location))
	var refs []string
	if e.Job {
		var exec struct {
			Template struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"template"`
		}
		if err := r.get(ctx, base+fmt.Sprintf("jobs/%s/executions/%s", url.PathEscape(e.Service), url.PathEscape(e.Revision)), &exec); err != nil {
			return nil, err
		}
		for _, c := range exec.Template.Containers {
			refs = append(refs, c.Image)
		}
	} else {
		var rev struct {
			ContainerStatuses []struct {
				ImageDigest string `json:"imageDigest"`
			} `json:"containerStatuses"`
		}
		if err := r.get(ctx, base+fmt.Sprintf("services/%s/revisions/%s", url.PathEscape(e.Service), url.PathEscape(e.Revision)), &rev); err != nil {
			return nil, err
		}
		for _, c := range rev.ContainerStatuses {
			refs = append(refs, c.ImageDigest)
		}
	}
	refs = slices.DeleteFunc(refs, func(ref string) bool { return ref == "" })
	if len(refs) == 0 {
		return nil, fmt.Errorf("cloudrun: %s %s has no image", workload(e), e.Revision)
	}
	return refs, nil
}

func (r *Resolver) ours(refs []string) (string, bool) {
	for _, ref := range refs {
		if r.Image == "" || repository(ref) == r.Image {
			return ref, true
		}
	}
	return "", false
}

// Strips the digest or tag: repo/app@sha256:… and repo/app:tag become repo/app.
func repository(ref string) string {
	name, _, _ := strings.Cut(ref, "@")
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i]
	}
	return name
}

// Many executions share one image, so the registry lookup is cached by digest.
func (r *Resolver) commitTagCached(ctx context.Context, ref string) (string, error) {
	if sha, ok := r.tags[ref]; ok {
		return sha, nil
	}
	img, err := parseImage(ref)
	if err != nil {
		return "", err
	}
	sha, err := r.commitTag(ctx, img)
	if err != nil {
		return "", err
	}
	if r.tags == nil {
		r.tags = map[string]string{}
	}
	r.tags[ref] = sha
	return sha, nil
}

func setKey(m map[string]bool, key string) map[string]bool {
	if m == nil {
		m = map[string]bool{}
	}
	m[key] = true
	return m
}

func (r *Resolver) commitTag(ctx context.Context, img image) (string, error) {
	u := or(r.RegistryEndpoint, defaultRegistryEndpoint) + fmt.Sprintf(
		"projects/%s/locations/%s/repositories/%s/packages/%s/versions/%s?view=FULL",
		url.PathEscape(img.project), url.PathEscape(img.location), url.PathEscape(img.repository),
		url.PathEscape(img.pkg), url.PathEscape(img.digest))
	var version struct {
		RelatedTags []struct {
			Name string `json:"name"`
		} `json:"relatedTags"`
	}
	if err := r.get(ctx, u, &version); err != nil {
		return "", err
	}
	for _, t := range version.RelatedTags {
		if tag := path.Base(t.Name); commitTag.MatchString(tag) {
			return tag, nil
		}
	}
	return "", fmt.Errorf("cloudrun: image %s has no commit tag", img.digest)
}

func (r *Resolver) get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cloudrun: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("cloudrun: GET %s: %s: %s", u, resp.Status, strings.TrimSpace(string(detail)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("cloudrun: decode %s: %w", u, err)
	}
	return nil
}

// us-central1-docker.pkg.dev/<project>/<repository>/<package...>@sha256:<hex>
func parseImage(ref string) (image, error) {
	name, digest, ok := strings.Cut(ref, "@")
	parts := strings.SplitN(name, "/", 4)
	if !ok || len(parts) != 4 || !strings.HasSuffix(parts[0], registryHostSuffix) || !strings.HasPrefix(digest, "sha256:") {
		return image{}, fmt.Errorf("cloudrun: %q is not an Artifact Registry image digest", ref)
	}
	return image{
		location:   strings.TrimSuffix(parts[0], registryHostSuffix),
		project:    parts[1],
		repository: parts[2],
		pkg:        parts[3],
		digest:     digest,
	}, nil
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
