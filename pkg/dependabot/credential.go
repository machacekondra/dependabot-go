package dependabot

// Credential is a set of values for authenticating against one host or
// registry, e.g. {"type": "git_source", "host": "github.com", "password": "..."}.
type Credential struct {
	values map[string]string
	// ReplacesBase reports whether this registry replaces the ecosystem's
	// default one rather than supplementing it.
	ReplacesBase bool
	// Scope narrows the credential to particular uses, when the job set one.
	Scope []string
}

// NewCredential builds a Credential from the flat map the job definition
// carries, pulling out the two structural keys.
func NewCredential(values map[string]string) *Credential {
	c := &Credential{values: map[string]string{}}
	for k, v := range values {
		switch k {
		case "replaces-base":
			c.ReplacesBase = v == "true"
		case "scope":
			c.Scope = []string{v}
		default:
			c.values[k] = v
		}
	}
	return c
}

// Get reads a credential field, returning "" when absent.
func (c *Credential) Get(key string) string {
	if c == nil {
		return ""
	}
	return c.values[key]
}

// Type is the credential's kind, e.g. "git_source" or "docker_registry".
func (c *Credential) Type() string { return c.Get("type") }

// Host is the host the credential applies to.
func (c *Credential) Host() string { return c.Get("host") }

// Map returns the credential's remaining fields.
func (c *Credential) Map() map[string]string {
	out := make(map[string]string, len(c.values))
	for k, v := range c.values {
		out[k] = v
	}
	return out
}

// CredentialsForType returns the credentials of a given type.
func CredentialsForType(creds []*Credential, credType string) []*Credential {
	var out []*Credential
	for _, c := range creds {
		if c.Type() == credType {
			out = append(out, c)
		}
	}
	return out
}

// CredentialForHost returns the first credential of the given type matching
// host, or nil.
func CredentialForHost(creds []*Credential, credType, host string) *Credential {
	for _, c := range creds {
		if c.Type() == credType && c.Host() == host {
			return c
		}
	}
	return nil
}
