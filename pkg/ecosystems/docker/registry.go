package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// DefaultRegistry is the host Docker Hub images resolve against, matching the
// default Ruby's CredentialsFinder uses.
const DefaultRegistry = "registry.hub.docker.com"

// Media types a manifest request accepts, most specific first. Sending all of
// them lets the registry pick the image index when one exists.
var manifestAcceptTypes = []string{
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
}

// tagsPageSize bounds each tag-listing request. Without it, registries such as
// Docker Hub try to return every tag at once and time out for images with very
// large tag counts.
const tagsPageSize = 100

// Registry is a Docker Registry HTTP API v2 client.
//
// Ruby uses the docker_registry2 gem. This is a direct implementation of the
// parts Dependabot needs: listing tags and resolving a reference to a digest,
// with the token handshake registries use for authentication.
type Registry struct {
	Host     string
	Username string
	Password string
	Client   *http.Client

	mu     sync.Mutex
	tokens map[string]string
}

// NewRegistry builds a client for a registry host, using the matching
// credential when one was supplied.
func NewRegistry(host string, credentials []*dependabot.Credential) *Registry {
	if host == "" {
		host = DefaultRegistry
	}
	r := &Registry{
		Host:   host,
		Client: &http.Client{Timeout: 60 * time.Second},
		tokens: map[string]string{},
	}
	if cred := registryCredential(host, credentials); cred != nil {
		r.Username = cred.Get("username")
		r.Password = cred.Get("password")
	}
	return r
}

// registryCredential finds the docker_registry credential for a host.
func registryCredential(host string, credentials []*dependabot.Credential) *dependabot.Credential {
	for _, cred := range credentials {
		if cred.Type() != "docker_registry" {
			continue
		}
		if cred.Get("registry") == host || cred.Host() == host {
			return cred
		}
	}
	return nil
}

// tagsResponse is the body of a tags/list request.
type tagsResponse struct {
	Tags []string `json:"tags"`
}

// Tags lists every tag of a repository, following the registry's pagination.
func (r *Registry) Tags(ctx context.Context, repo string) ([]string, error) {
	next := fmt.Sprintf("/v2/%s/tags/list?n=%d", repo, tagsPageSize)

	var all []string
	// Registries have been known to return a self-referential Link header;
	// bound the walk so a bad one cannot loop forever.
	for page := 0; next != "" && page < 1000; page++ {
		resp, err := r.do(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}

		var body tagsResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if decodeErr != nil {
			return nil, &dependabot.PrivateSourceBadResponseError{Source: r.Host}
		}
		all = append(all, body.Tags...)

		next = nextPageLink(link)
	}
	return all, nil
}

var linkRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="?next"?`)

// nextPageLink reads the next page's path out of a Link header.
func nextPageLink(header string) string {
	m := linkRe.FindStringSubmatch(header)
	if m == nil {
		return ""
	}
	return m[1]
}

// ManifestDigest resolves a tag or digest reference to the manifest digest the
// registry currently serves, without the "sha256:" prefix. It returns an empty
// string when the reference does not exist.
func (r *Registry) ManifestDigest(ctx context.Context, repo, reference string) (string, error) {
	path := fmt.Sprintf("/v2/%s/manifests/%s", repo, reference)

	resp, err := r.do(ctx, http.MethodHead, path, manifestAcceptTypes)
	if err != nil {
		var notFound *dependabot.RegistryError
		if ok := asRegistryError(err, &notFound); ok && notFound.StatusCode == http.StatusNotFound {
			return "", nil
		}
		return "", err
	}
	defer resp.Body.Close()

	return strings.TrimPrefix(resp.Header.Get("Docker-Content-Digest"), "sha256:"), nil
}

func asRegistryError(err error, target **dependabot.RegistryError) bool {
	e, ok := err.(*dependabot.RegistryError)
	if ok {
		*target = e
	}
	return ok
}

// do issues a request, performing the bearer-token handshake once if the
// registry asks for one.
func (r *Registry) do(ctx context.Context, method, path string, accept []string) (*http.Response, error) {
	resp, err := r.send(ctx, method, path, accept, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return r.checkStatus(resp)
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	resp.Body.Close()

	token, err := r.tokenFor(ctx, challenge)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, &dependabot.PrivateSourceAuthFailureError{Source: r.Host}
	}

	resp, err = r.send(ctx, method, path, accept, token)
	if err != nil {
		return nil, err
	}
	return r.checkStatus(resp)
}

func (r *Registry) send(ctx context.Context, method, path string, accept []string, token string) (*http.Response, error) {
	target := path
	if !strings.HasPrefix(target, "http") {
		target = "https://" + r.Host + target
	}

	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	for _, mediaType := range accept {
		req.Header.Add("Accept", mediaType)
	}
	switch {
	case token != "":
		req.Header.Set("Authorization", "Bearer "+token)
	case r.Username != "":
		req.SetBasicAuth(r.Username, r.Password)
	}

	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, &dependabot.PrivateSourceTimedOutError{Source: r.Host}
	}
	return resp, nil
}

func (r *Registry) checkStatus(resp *http.Response) (*http.Response, error) {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &dependabot.PrivateSourceAuthFailureError{Source: r.Host}
	case http.StatusTooManyRequests:
		return nil, &dependabot.PrivateSourceBadResponseError{Source: r.Host}
	default:
		return nil, &dependabot.RegistryError{
			StatusCode: resp.StatusCode,
			Detail:     fmt.Sprintf("%s returned %s", r.Host, resp.Status),
		}
	}
}

// tokenFor completes a Bearer challenge, caching the token per scope so a run
// that touches many tags authenticates once.
func (r *Registry) tokenFor(ctx context.Context, challenge string) (string, error) {
	realm, params := parseChallenge(challenge)
	if realm == "" {
		return "", nil
	}
	scope := params["scope"]

	r.mu.Lock()
	cached, ok := r.tokens[scope]
	r.mu.Unlock()
	if ok {
		return cached, nil
	}

	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	query := tokenURL.Query()
	for _, key := range []string{"service", "scope"} {
		if value := params[key]; value != "" {
			query.Set(key, value)
		}
	}
	tokenURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", err
	}
	if r.Username != "" {
		req.SetBasicAuth(r.Username, r.Password)
	}

	resp, err := r.Client.Do(req)
	if err != nil {
		return "", &dependabot.PrivateSourceTimedOutError{Source: r.Host}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", &dependabot.PrivateSourceAuthFailureError{Source: r.Host}
	}

	// Registries return the value under "token"; some older ones use
	// "access_token" instead.
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", &dependabot.PrivateSourceBadResponseError{Source: r.Host}
	}
	token := body.Token
	if token == "" {
		token = body.AccessToken
	}

	r.mu.Lock()
	r.tokens[scope] = token
	r.mu.Unlock()

	return token, nil
}

var challengeParamRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

// parseChallenge reads a `Bearer realm="…",service="…",scope="…"` header.
func parseChallenge(header string) (realm string, params map[string]string) {
	params = map[string]string{}
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return "", params
	}
	for _, m := range challengeParamRe.FindAllStringSubmatch(header, -1) {
		params[m[1]] = m[2]
	}
	return params["realm"], params
}
