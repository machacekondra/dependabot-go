package gomodules

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// fakeProxy serves the two module proxy endpoints the checker uses.
type fakeProxy struct {
	versions map[string][]string
	info     map[string]time.Time
	requests int
}

func (f *fakeProxy) start(t *testing.T) *Proxy {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests++
		path := strings.TrimPrefix(r.URL.Path, "/")

		if modulePath, ok := strings.CutSuffix(path, "/@v/list"); ok {
			versions, known := f.versions[modulePath]
			if !known {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(strings.Join(versions, "\n") + "\n"))
			return
		}

		if rest, ok := strings.CutSuffix(path, ".info"); ok {
			modulePath, version, _ := strings.Cut(rest, "/@v/")
			published, known := f.info[modulePath+"@"+version]
			if !known {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(`{"Version":"` + version + `","Time":"` +
				published.Format(time.RFC3339) + `"}`))
			return
		}

		t.Errorf("unexpected proxy request: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	return &Proxy{BaseURL: server.URL, Client: server.Client()}
}

func TestProxyVersions(t *testing.T) {
	fake := &fakeProxy{versions: map[string][]string{
		"github.com/spf13/cobra": {"v1.8.0", "v1.8.1", "v1.9.0"},
	}}
	proxy := fake.start(t)

	got, err := proxy.Versions(context.Background(), "github.com/spf13/cobra")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if strings.Join(got, ",") != "v1.8.0,v1.8.1,v1.9.0" {
		t.Errorf("Versions = %v, want the three published versions", got)
	}
}

// Upper-case letters in a module path are escaped as "!" plus the lower-case
// letter, so a case-insensitive filesystem cannot confuse two modules.
func TestProxyEscapesModulePath(t *testing.T) {
	fake := &fakeProxy{versions: map[string][]string{
		"github.com/!burnt!sushi/toml": {"v1.4.0"},
	}}
	proxy := fake.start(t)

	got, err := proxy.Versions(context.Background(), "github.com/BurntSushi/toml")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 1 || got[0] != "v1.4.0" {
		t.Errorf("Versions = %v, want v1.4.0", got)
	}
}

func TestProxyInfo(t *testing.T) {
	published := time.Date(2026, 3, 20, 11, 1, 6, 0, time.UTC)
	fake := &fakeProxy{info: map[string]time.Time{
		"golang.org/x/mod@v0.41.0": published,
	}}
	proxy := fake.start(t)

	info, err := proxy.Info(context.Background(), "golang.org/x/mod", "v0.41.0")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if !info.Published.Equal(published) {
		t.Errorf("Published = %v, want %v", info.Published, published)
	}
}

func TestProxyUnknownModule(t *testing.T) {
	proxy := (&fakeProxy{versions: map[string][]string{}}).start(t)

	_, err := proxy.Versions(context.Background(), "example.com/nope")
	if err == nil {
		t.Fatal("expected an error for an unknown module")
	}
	var notResolvable *dependabot.FileNotResolvableError
	if !errors.As(err, &notResolvable) {
		t.Errorf("error = %T (%v), want FileNotResolvableError", err, err)
	}
}

func TestProxyAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	proxy := &Proxy{BaseURL: server.URL, Client: server.Client()}

	_, err := proxy.Versions(context.Background(), "example.com/private")
	var authFailure *dependabot.PrivateSourceAuthFailureError
	if !errors.As(err, &authFailure) {
		t.Errorf("error = %T (%v), want PrivateSourceAuthFailureError", err, err)
	}
}

func TestProxyURLFromEnv(t *testing.T) {
	cases := map[string]string{
		"":                                 DefaultProxyURL,
		"off":                              DefaultProxyURL,
		"direct":                           DefaultProxyURL,
		"https://proxy.example.com/":       "https://proxy.example.com",
		"off,https://proxy.example.com":    "https://proxy.example.com",
		"https://proxy.example.com,direct": "https://proxy.example.com",
	}
	for env, want := range cases {
		t.Setenv("GOPROXY", env)
		if got := proxyURLFromEnv(); got != want {
			t.Errorf("GOPROXY=%q gave %q, want %q", env, got, want)
		}
	}
}
