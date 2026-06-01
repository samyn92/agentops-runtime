/*
Package gitlab is a self-contained wrapper around the official GitLab Go SDK
(gitlab.com/gitlab-org/api/client-go) used by the agent runtime's native
gitlab_* tools.

It is intentionally standalone: the runtime does NOT import agentops-core. All
configuration arrives via environment variables injected by the operator
(GitLabEnvFromIntegrations):

	GITLAB_TOKEN     (required) group/service-account access token
	GITLAB_URL       base URL (default https://gitlab.com)
	GITLAB_GROUP     group full path (when bound to a gitlab-group Integration)
	GITLAB_PROJECT   single project path/ID (when bound to a gitlab-project)
	GITLAB_PROJECTS  comma-separated allow-list of project paths/IDs
	GITLAB_READONLY  "true" disables all mutating operations

Two safety controls are enforced here regardless of agent prompt:
  - ReadOnly: write methods return ErrReadOnly when GITLAB_READONLY=true.
  - Project allow-list: when GITLAB_PROJECTS is set, any operation targeting a
    project outside the list returns ErrProjectNotAllowed. This bounds the blast
    radius of a group-scoped token to an explicit set of projects.
*/
package gitlab

import (
	"errors"
	"fmt"
	"os"
	"strings"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// ErrReadOnly is returned by mutating methods when the client is read-only.
var ErrReadOnly = errors.New("gitlab: client is read-only (GITLAB_READONLY=true); write operations are disabled")

// ErrProjectNotAllowed is returned when an operation targets a project that is
// not in the configured GITLAB_PROJECTS allow-list.
var ErrProjectNotAllowed = errors.New("gitlab: project is not in the allowed project list (GITLAB_PROJECTS)")

// ErrNotConfigured is returned by NewFromEnv when no GITLAB_TOKEN is present.
var ErrNotConfigured = errors.New("gitlab: GITLAB_TOKEN not set")

// Client wraps the official GitLab client with runtime safety controls.
type Client struct {
	api      *gl.Client
	baseURL  string
	group    string
	project  string
	allowed  []string // normalized project allow-list (empty = allow all)
	readOnly bool
}

// Config configures a Client. Use NewFromEnv for the operator-injected path.
type Config struct {
	Token    string
	BaseURL  string
	Group    string
	Project  string
	Projects []string
	ReadOnly bool
}

// IsConfigured reports whether GITLAB_TOKEN is present in the environment.
func IsConfigured() bool { return os.Getenv("GITLAB_TOKEN") != "" }

// NewFromEnv builds a Client from the operator-injected environment variables.
// Returns ErrNotConfigured when GITLAB_TOKEN is unset.
func NewFromEnv() (*Client, error) {
	token := os.Getenv("GITLAB_TOKEN")
	if token == "" {
		return nil, ErrNotConfigured
	}
	var projects []string
	if v := strings.TrimSpace(os.Getenv("GITLAB_PROJECTS")); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				projects = append(projects, p)
			}
		}
	}
	return New(Config{
		Token:    token,
		BaseURL:  os.Getenv("GITLAB_URL"),
		Group:    os.Getenv("GITLAB_GROUP"),
		Project:  os.Getenv("GITLAB_PROJECT"),
		Projects: projects,
		ReadOnly: strings.EqualFold(os.Getenv("GITLAB_READONLY"), "true"),
	})
}

// New builds a Client from an explicit Config.
func New(cfg Config) (*Client, error) {
	if cfg.Token == "" {
		return nil, ErrNotConfigured
	}
	base := cfg.BaseURL
	if base == "" {
		base = "https://gitlab.com"
	}
	api, err := gl.NewClient(cfg.Token, gl.WithBaseURL(base))
	if err != nil {
		return nil, fmt.Errorf("gitlab: new client: %w", err)
	}
	allowed := make([]string, 0, len(cfg.Projects))
	for _, p := range cfg.Projects {
		if p = strings.TrimSpace(p); p != "" {
			allowed = append(allowed, normalizeProject(p))
		}
	}
	return &Client{
		api:      api,
		baseURL:  base,
		group:    cfg.Group,
		project:  cfg.Project,
		allowed:  allowed,
		readOnly: cfg.ReadOnly,
	}, nil
}

// API exposes the underlying official client for advanced callers.
func (c *Client) API() *gl.Client { return c.api }

// BaseURL returns the configured GitLab base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// Group returns the configured group full path (may be empty).
func (c *Client) Group() string { return c.group }

// DefaultProject returns the single bound project (may be empty).
func (c *Client) DefaultProject() string { return c.project }

// ReadOnly reports whether mutating operations are disabled.
func (c *Client) ReadOnly() bool { return c.readOnly }

// resolveProject returns the effective project for an operation: the explicit
// argument if given, otherwise the single bound DefaultProject. It enforces the
// allow-list. An empty result with nil error is impossible — callers always get
// either a usable project or an error.
func (c *Client) resolveProject(project string) (string, error) {
	p := strings.TrimSpace(project)
	if p == "" {
		p = c.project
	}
	if p == "" {
		return "", errors.New("gitlab: no project specified and no default project bound")
	}
	if err := c.checkAllowed(p); err != nil {
		return "", err
	}
	return p, nil
}

// checkAllowed enforces the GITLAB_PROJECTS allow-list. When the list is empty,
// all projects are allowed (the token's own scope is the boundary).
func (c *Client) checkAllowed(project string) error {
	if len(c.allowed) == 0 {
		return nil
	}
	want := normalizeProject(project)
	for _, a := range c.allowed {
		if a == want {
			return nil
		}
	}
	return fmt.Errorf("%w: %q (allowed: %s)", ErrProjectNotAllowed, project, strings.Join(c.allowed, ", "))
}

// requireWrite returns ErrReadOnly when the client is read-only.
func (c *Client) requireWrite() error {
	if c.readOnly {
		return ErrReadOnly
	}
	return nil
}

// normalizeProject lower-cases and trims a project path for comparison. Numeric
// IDs and full paths are compared as-is (lower-cased), which matches GitLab's
// case-insensitive path semantics.
func normalizeProject(p string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(p), "/"))
}
