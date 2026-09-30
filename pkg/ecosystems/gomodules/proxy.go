package gomodules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/mod/module"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// DefaultProxyURL is the proxy used when GOPROXY says nothing usable.
const DefaultProxyURL = "https://proxy.golang.org"

// Proxy is a client for the Go module proxy protocol
// (https://go.dev/ref/mod#module-proxy).
//
// Ruby shells out to `go list -m -versions -json`, which needs a Go toolchain
// in the image and a scratch module directory. Speaking the proxy's HTTP
// protocol directly removes both, at the cost of not resolving modules that
// are only reachable over direct VCS access.
type Proxy struct {
	BaseURL string
	Client  *http.Client
}

// NewProxy builds a proxy client honouring GOPROXY, falling back to the public
// proxy when it is unset, "direct" or "off".
func NewProxy() *Proxy {
	return &Proxy{
		BaseURL: proxyURLFromEnv(),
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func proxyURLFromEnv() string {
	for _, candidate := range strings.Split(os.Getenv("GOPROXY"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || candidate == "direct" || candidate == "off" {
			continue
		}
		return strings.TrimRight(candidate, "/")
	}
	return DefaultProxyURL
}

// ReleaseInfo is one entry from the proxy's version list, with the publication
// time when the caller asked for it.
type ReleaseInfo struct {
	Version   string
	Published time.Time
}

// Versions lists the module's tagged versions. Pseudo-versions are not listed
// by the proxy, matching `go list -m -versions`.
func (p *Proxy) Versions(ctx context.Context, modulePath string) ([]string, error) {
	escaped, err := module.EscapePath(modulePath)
	if err != nil {
		return nil, fmt.Errorf("escaping module path %q: %w", modulePath, err)
	}

	body, err := p.get(ctx, fmt.Sprintf("%s/%s/@v/list", p.BaseURL, escaped))
	if err != nil {
		return nil, err
	}

	var versions []string
	for _, line := range strings.Split(string(body), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			versions = append(versions, line)
		}
	}
	return versions, nil
}

// Info returns the publication time of a specific version, which the cooldown
// filter uses to leave freshly-published releases alone.
func (p *Proxy) Info(ctx context.Context, modulePath, version string) (*ReleaseInfo, error) {
	escaped, err := module.EscapePath(modulePath)
	if err != nil {
		return nil, fmt.Errorf("escaping module path %q: %w", modulePath, err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		return nil, fmt.Errorf("escaping version %q: %w", version, err)
	}

	body, err := p.get(ctx, fmt.Sprintf("%s/%s/@v/%s.info", p.BaseURL, escaped, escapedVersion))
	if err != nil {
		return nil, err
	}

	var info struct {
		Version string    `json:"Version"`
		Time    time.Time `json:"Time"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parsing proxy info for %s@%s: %w", modulePath, version, err)
	}
	return &ReleaseInfo{Version: info.Version, Published: info.Time}, nil
}

func (p *Proxy) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, &dependabot.PrivateSourceTimedOutError{Source: p.BaseURL}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, &dependabot.FileNotResolvableError{
			Detail: fmt.Sprintf("module not found on %s", p.BaseURL),
		}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &dependabot.PrivateSourceAuthFailureError{Source: p.BaseURL}
	case resp.StatusCode != http.StatusOK:
		return nil, &dependabot.RegistryError{StatusCode: resp.StatusCode, Detail: url}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &dependabot.PrivateSourceBadResponseError{Source: p.BaseURL}
	}
	return body, nil
}
