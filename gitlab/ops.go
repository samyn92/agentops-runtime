package gitlab

import (
	"fmt"
	"strings"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// ── Projects / Groups ───────────────────────────────────────────────────────

// GetProject returns project metadata. Enforces the allow-list.
func (c *Client) GetProject(project string) (*gl.Project, error) {
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	p, _, err := c.api.Projects.GetProject(pid, &gl.GetProjectOptions{})
	return p, err
}

// ListGroupProjects lists projects within the bound (or given) group. When the
// allow-list is set, results are filtered to allowed projects.
func (c *Client) ListGroupProjects(group string, search string) ([]*gl.Project, error) {
	gid := strings.TrimSpace(group)
	if gid == "" {
		gid = c.group
	}
	if gid == "" {
		return nil, fmt.Errorf("gitlab: no group specified and no default group bound")
	}
	opt := &gl.ListGroupProjectsOptions{
		IncludeSubGroups: gl.Ptr(true),
		ListOptions:      gl.ListOptions{PerPage: 100},
	}
	if search != "" {
		opt.Search = gl.Ptr(search)
	}
	projects, _, err := c.api.Groups.ListGroupProjects(gid, opt)
	if err != nil {
		return nil, err
	}
	return c.filterAllowedProjects(projects), nil
}

// SearchProjects performs a project search, scoped to the bound group when one
// is configured. Results are filtered through the allow-list.
func (c *Client) SearchProjects(query string) ([]*gl.Project, error) {
	opt := &gl.SearchOptions{ListOptions: gl.ListOptions{PerPage: 50}}
	var (
		projects []*gl.Project
		err      error
	)
	if c.group != "" {
		projects, _, err = c.api.Search.ProjectsByGroup(c.group, query, opt)
	} else {
		projects, _, err = c.api.Search.Projects(query, opt)
	}
	if err != nil {
		return nil, err
	}
	return c.filterAllowedProjects(projects), nil
}

func (c *Client) filterAllowedProjects(in []*gl.Project) []*gl.Project {
	if len(c.allowed) == 0 {
		return in
	}
	out := in[:0:0]
	for _, p := range in {
		if p == nil {
			continue
		}
		if c.checkAllowed(p.PathWithNamespace) == nil {
			out = append(out, p)
		}
	}
	return out
}

// ── Merge Requests ──────────────────────────────────────────────────────────

// ListMergeRequests lists MRs for a project. state: opened|closed|merged|all.
func (c *Client) ListMergeRequests(project, state string) ([]*gl.BasicMergeRequest, error) {
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	if state == "" {
		state = "opened"
	}
	opt := &gl.ListProjectMergeRequestsOptions{
		State:       gl.Ptr(state),
		ListOptions: gl.ListOptions{PerPage: 50},
	}
	mrs, _, err := c.api.MergeRequests.ListProjectMergeRequests(pid, opt)
	return mrs, err
}

// GetMergeRequest returns a single MR by IID.
func (c *Client) GetMergeRequest(project string, iid int64) (*gl.MergeRequest, error) {
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	mr, _, err := c.api.MergeRequests.GetMergeRequest(pid, iid, &gl.GetMergeRequestsOptions{})
	return mr, err
}

// GetMergeRequestDiffs returns the per-file diffs of an MR.
func (c *Client) GetMergeRequestDiffs(project string, iid int64) ([]*gl.MergeRequestDiff, error) {
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	diffs, _, err := c.api.MergeRequests.ListMergeRequestDiffs(pid, iid, &gl.ListMergeRequestDiffsOptions{})
	return diffs, err
}

// CreateMergeRequest opens a new MR. Write-gated.
func (c *Client) CreateMergeRequest(project, title, description, sourceBranch, targetBranch string) (*gl.MergeRequest, error) {
	if err := c.requireWrite(); err != nil {
		return nil, err
	}
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	opt := &gl.CreateMergeRequestOptions{
		Title:        gl.Ptr(title),
		SourceBranch: gl.Ptr(sourceBranch),
		TargetBranch: gl.Ptr(targetBranch),
	}
	if description != "" {
		opt.Description = gl.Ptr(description)
	}
	mr, _, err := c.api.MergeRequests.CreateMergeRequest(pid, opt)
	return mr, err
}

// UpdateMergeRequest edits an MR's title/description/labels. Write-gated.
func (c *Client) UpdateMergeRequest(project string, iid int64, title, description, labels string) (*gl.MergeRequest, error) {
	if err := c.requireWrite(); err != nil {
		return nil, err
	}
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	opt := &gl.UpdateMergeRequestOptions{}
	if title != "" {
		opt.Title = gl.Ptr(title)
	}
	if description != "" {
		opt.Description = gl.Ptr(description)
	}
	if labels != "" {
		lo := gl.LabelOptions(splitCSV(labels))
		opt.Labels = &lo
	}
	mr, _, err := c.api.MergeRequests.UpdateMergeRequest(pid, iid, opt)
	return mr, err
}

// AddMergeRequestNote posts a comment on an MR. Write-gated.
func (c *Client) AddMergeRequestNote(project string, iid int64, body string) (*gl.Note, error) {
	if err := c.requireWrite(); err != nil {
		return nil, err
	}
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	n, _, err := c.api.Notes.CreateMergeRequestNote(pid, iid, &gl.CreateMergeRequestNoteOptions{Body: gl.Ptr(body)})
	return n, err
}

// ── Issues ──────────────────────────────────────────────────────────────────

// ListIssues lists issues for a project. state: opened|closed|all.
func (c *Client) ListIssues(project, state, labels string) ([]*gl.Issue, error) {
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	if state == "" {
		state = "opened"
	}
	opt := &gl.ListProjectIssuesOptions{
		State:       gl.Ptr(state),
		ListOptions: gl.ListOptions{PerPage: 50},
	}
	if labels != "" {
		lo := gl.LabelOptions(splitCSV(labels))
		opt.Labels = &lo
	}
	issues, _, err := c.api.Issues.ListProjectIssues(pid, opt)
	return issues, err
}

// GetIssue returns a single issue by IID.
func (c *Client) GetIssue(project string, iid int64) (*gl.Issue, error) {
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	issue, _, err := c.api.Issues.GetIssue(pid, iid)
	return issue, err
}

// AddIssueNote posts a comment on an issue. Write-gated.
func (c *Client) AddIssueNote(project string, iid int64, body string) (*gl.Note, error) {
	if err := c.requireWrite(); err != nil {
		return nil, err
	}
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	n, _, err := c.api.Notes.CreateIssueNote(pid, iid, &gl.CreateIssueNoteOptions{Body: gl.Ptr(body)})
	return n, err
}

// ── Pipelines ───────────────────────────────────────────────────────────────

// ListPipelines lists recent pipelines for a project, optionally filtered by ref.
func (c *Client) ListPipelines(project, ref string) ([]*gl.PipelineInfo, error) {
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	opt := &gl.ListProjectPipelinesOptions{ListOptions: gl.ListOptions{PerPage: 20}}
	if ref != "" {
		opt.Ref = gl.Ptr(ref)
	}
	pipes, _, err := c.api.Pipelines.ListProjectPipelines(pid, opt)
	return pipes, err
}

// GetPipeline returns a single pipeline by ID.
func (c *Client) GetPipeline(project string, id int64) (*gl.Pipeline, error) {
	pid, err := c.resolveProject(project)
	if err != nil {
		return nil, err
	}
	p, _, err := c.api.Pipelines.GetPipeline(pid, id)
	return p, err
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
