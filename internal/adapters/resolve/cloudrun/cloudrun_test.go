package cloudrun

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

const digestRef = "us-central1-docker.pkg.dev/baseline-shared/internal-api/internal-api@sha256:6c93"

type fakeAPI struct {
	bodies map[string]string
	calls  []string
}

func (f *fakeAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	u := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		u += "?" + r.URL.RawQuery
	}
	f.calls = append(f.calls, u)
	body, ok := f.bodies[u]
	status := http.StatusOK
	if !ok {
		status, body = http.StatusNotFound, `{"error":"not found"}`
	}
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body))}, nil
}

const (
	revisionPath = "/v2/projects/divdot-develop/locations/northamerica-northeast1/services/divdot/revisions/divdot-00023-8b9"
	versionPath  = "/v1/projects/baseline-shared/locations/us-central1/repositories/internal-api/packages/internal-api/versions/sha256:6c93?view=FULL"
)

func newResolver(api *fakeAPI) *Resolver {
	return &Resolver{Client: &http.Client{Transport: api}}
}

func ev() event.Event {
	return event.Event{Project: "divdot-develop", Location: "northamerica-northeast1", Service: "divdot", Revision: "divdot-00023-8b9"}
}

func TestResolvesRevisionToCommitTagAndCaches(t *testing.T) {
	api := &fakeAPI{bodies: map[string]string{
		revisionPath: `{"containers":[{"image":"internal-api:develop"}],"containerStatuses":[{"name":"api","imageDigest":"` + digestRef + `"}]}`,
		versionPath: `{"relatedTags":[
			{"name":"projects/baseline-shared/locations/us-central1/repositories/internal-api/packages/internal-api/tags/develop"},
			{"name":"projects/baseline-shared/locations/us-central1/repositories/internal-api/packages/internal-api/tags/6585ecf"}]}`,
	}}
	r := newResolver(api)

	for range 2 {
		sha, err := r.SHA(context.Background(), ev())
		if err != nil {
			t.Fatal(err)
		}
		if sha != "6585ecf" {
			t.Errorf("sha = %q, want 6585ecf", sha)
		}
	}
	if len(api.calls) != 2 {
		t.Errorf("made %d API calls over two lookups, want 2 (second lookup cached)", len(api.calls))
	}
}

func TestSameRevisionNameInAnotherProjectIsResolvedSeparately(t *testing.T) {
	api := &fakeAPI{bodies: map[string]string{
		revisionPath: `{"containerStatuses":[{"imageDigest":"` + digestRef + `"}]}`,
		versionPath:  `{"relatedTags":[{"name":"x/tags/6585ecf"}]}`,
	}}
	r := newResolver(api)
	if _, err := r.SHA(context.Background(), ev()); err != nil {
		t.Fatal(err)
	}

	prod := ev()
	prod.Project = "divdot-production"
	if _, err := r.SHA(context.Background(), prod); err == nil {
		t.Error("want a lookup (and here a 404) for the production revision, not the develop cache entry")
	}
}

func TestResolutionErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"revision not found":    {},
		"no image digest":       {revisionPath: `{"containerStatuses":[]}`},
		"not artifact registry": {revisionPath: `{"containerStatuses":[{"imageDigest":"docker.io/library/app@sha256:6c93"}]}`},
		"no commit tag": {
			revisionPath: `{"containerStatuses":[{"imageDigest":"` + digestRef + `"}]}`,
			versionPath:  `{"relatedTags":[{"name":"x/tags/develop"},{"name":"x/tags/latest"}]}`,
		},
	}
	for name, bodies := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := newResolver(&fakeAPI{bodies: bodies}).SHA(context.Background(), ev()); err == nil {
				t.Error("want error")
			}
		})
	}
}

func TestParseImageEscapesNestedPackageNames(t *testing.T) {
	img, err := parseImage("europe-west1-docker.pkg.dev/proj/repo/team/app@sha256:abc")
	if err != nil {
		t.Fatal(err)
	}
	if img != (image{location: "europe-west1", project: "proj", repository: "repo", pkg: "team/app", digest: "sha256:abc"}) {
		t.Errorf("got %+v", img)
	}

	api := &fakeAPI{bodies: map[string]string{
		revisionPath: `{"containerStatuses":[{"imageDigest":"us-central1-docker.pkg.dev/proj/repo/team/app@sha256:abc"}]}`,
	}}
	newResolver(api).SHA(context.Background(), ev())
	if want := "/v1/projects/proj/locations/us-central1/repositories/repo/packages/team%2Fapp/versions/sha256:abc?view=FULL"; api.calls[1] != want {
		t.Errorf("registry call = %s, want %s", api.calls[1], want)
	}
}

const ourImage = "us-central1-docker.pkg.dev/baseline-shared/internal-api/internal-api"

func jobEv(execution string) event.Event {
	return event.Event{Project: "divdot-develop", Location: "northamerica-northeast1", Service: "payouts", Revision: execution, Job: true}
}

func executionPath(execution string) string {
	return "/v2/projects/divdot-develop/locations/northamerica-northeast1/jobs/payouts/executions/" + execution
}

func TestJobExecutionsResolveAndShareTheRegistryLookup(t *testing.T) {
	execution := `{"template":{"containers":[{"image":"` + digestRef + `"}]}}`
	api := &fakeAPI{bodies: map[string]string{
		executionPath("payouts-a"): execution,
		executionPath("payouts-b"): execution,
		versionPath:                `{"relatedTags":[{"name":"x/tags/6585ecf"}]}`,
	}}
	r := newResolver(api)
	r.Image = ourImage

	for _, exec := range []string{"payouts-a", "payouts-b"} {
		sha, err := r.SHA(context.Background(), jobEv(exec))
		if err != nil || sha != "6585ecf" {
			t.Fatalf("%s: sha = %q, %v", exec, sha, err)
		}
	}
	if len(api.calls) != 3 {
		t.Errorf("made %d calls, want 3: two executions and one registry lookup", len(api.calls))
	}
}

func TestWorkloadsRunningOtherImagesAreOtherCode(t *testing.T) {
	cases := map[string]string{
		"another repository": `{"template":{"containers":[{"image":"us-central1-docker.pkg.dev/baseline-shared/bank-engine/bank-engine@sha256:6e0b"}]}}`,
		"docker hub":         `{"template":{"containers":[{"image":"docker.io/tryretool/backend@sha256:ff97"}]}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			api := &fakeAPI{bodies: map[string]string{executionPath("get-1"): body}}
			r := newResolver(api)
			r.Image = ourImage
			for range 2 {
				if _, err := r.SHA(context.Background(), jobEv("get-1")); !errors.Is(err, pipeline.ErrOtherCode) {
					t.Fatalf("err = %v, want ErrOtherCode", err)
				}
			}
			if len(api.calls) != 1 {
				t.Errorf("made %d calls, want the answer cached after 1", len(api.calls))
			}
		})
	}
}

func TestRepository(t *testing.T) {
	for ref, want := range map[string]string{
		ourImage + "@sha256:abc":        ourImage,
		ourImage + ":develop":           ourImage,
		ourImage:                        ourImage,
		"localhost:5000/app:v1":         "localhost:5000/app",
		"localhost:5000/app@sha256:abc": "localhost:5000/app",
	} {
		if got := repository(ref); got != want {
			t.Errorf("repository(%q) = %q, want %q", ref, got, want)
		}
	}
}
