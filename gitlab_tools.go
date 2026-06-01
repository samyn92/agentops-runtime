/*
Agent Runtime — Fantasy (Go)

Native GitLab tools. These replace the OCI-packaged mcp-gitlab server with
direct calls to the official GitLab Go SDK via the self-contained gitlab
package. Tools are auto-enabled when GITLAB_TOKEN is present (injected by the
operator from a bound gitlab-group / gitlab-project Integration). Write tools
are registered only when the client is read-write (GITLAB_READONLY != "true").
*/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"charm.land/fantasy"
	"github.com/samyn92/agentops-runtime-fantasy/gitlab"
)

// gitlabClient is the process-wide GitLab client, initialized in gitlabTools.
var gitlabClient *gitlab.Client

// jsonResponse marshals v to indented JSON as a tool text response.
func jsonResponse(v any) (fantasy.ToolResponse, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return fantasy.NewTextResponse(string(b)), nil
}

// glErr converts a client error into an appropriate tool response. Allow-list
// and read-only violations are surfaced as plain (non-Go) errors so the model
// understands the boundary rather than treating it as a transient failure.
func glErr(err error) (fantasy.ToolResponse, error) {
	return fantasy.NewTextErrorResponse(err.Error()), nil
}

// ── Read tools ──────────────────────────────────────────────────────────────

type glProjectInput struct {
	Project string `json:"project,omitempty" description:"Project path (group/repo) or numeric ID. Omit to use the agent's bound project."`
}

func newGitLabGetProjectTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_get_project",
		"Get GitLab project info (description, visibility, default branch, web URL).",
		func(_ context.Context, in glProjectInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			p, err := gitlabClient.GetProject(in.Project)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(p)
		})
}

type glListGroupProjectsInput struct {
	Group  string `json:"group,omitempty" description:"Group full path. Omit to use the agent's bound group."`
	Search string `json:"search,omitempty" description:"Optional name filter."`
}

func newGitLabListGroupProjectsTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_list_group_projects",
		"List projects within a GitLab group (includes subgroups). Filtered to the agent's allowed projects.",
		func(_ context.Context, in glListGroupProjectsInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			projects, err := gitlabClient.ListGroupProjects(in.Group, in.Search)
			if err != nil {
				return glErr(err)
			}
			out := make([]map[string]any, 0, len(projects))
			for _, p := range projects {
				out = append(out, map[string]any{
					"id":   p.ID,
					"path": p.PathWithNamespace,
					"name": p.Name,
					"url":  p.WebURL,
				})
			}
			return jsonResponse(out)
		})
}

type glSearchInput struct {
	Query string `json:"query" description:"Search query for project names/paths."`
}

func newGitLabSearchTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_search",
		"Search GitLab projects (scoped to the agent's bound group when configured). Filtered to allowed projects.",
		func(_ context.Context, in glSearchInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			projects, err := gitlabClient.SearchProjects(in.Query)
			if err != nil {
				return glErr(err)
			}
			out := make([]map[string]any, 0, len(projects))
			for _, p := range projects {
				out = append(out, map[string]any{
					"id":   p.ID,
					"path": p.PathWithNamespace,
					"name": p.Name,
					"url":  p.WebURL,
				})
			}
			return jsonResponse(out)
		})
}

type glListMRsInput struct {
	Project string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	State   string `json:"state,omitempty" description:"MR state: opened (default) / closed / merged / all."`
}

func newGitLabListMRsTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_list_mrs",
		"List merge requests for a project.",
		func(_ context.Context, in glListMRsInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			mrs, err := gitlabClient.ListMergeRequests(in.Project, in.State)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(mrs)
		})
}

type glMRInput struct {
	Project string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	IID     int64  `json:"iid" description:"Merge request IID (project-scoped number)."`
}

func newGitLabGetMRTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_get_mr",
		"Get details of a specific merge request.",
		func(_ context.Context, in glMRInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			mr, err := gitlabClient.GetMergeRequest(in.Project, in.IID)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(mr)
		})
}

func newGitLabGetMRDiffTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_get_mr_diff",
		"Get the per-file diffs/changes of a merge request.",
		func(_ context.Context, in glMRInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			diffs, err := gitlabClient.GetMergeRequestDiffs(in.Project, in.IID)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(diffs)
		})
}

type glListIssuesInput struct {
	Project string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	State   string `json:"state,omitempty" description:"Issue state: opened (default) / closed / all."`
	Labels  string `json:"labels,omitempty" description:"Comma-separated label filter."`
}

func newGitLabListIssuesTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_list_issues",
		"List issues for a project.",
		func(_ context.Context, in glListIssuesInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			issues, err := gitlabClient.ListIssues(in.Project, in.State, in.Labels)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(issues)
		})
}

type glIssueInput struct {
	Project string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	IID     int64  `json:"iid" description:"Issue IID (project-scoped number)."`
}

func newGitLabGetIssueTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_get_issue",
		"Get details of a specific issue.",
		func(_ context.Context, in glIssueInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			issue, err := gitlabClient.GetIssue(in.Project, in.IID)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(issue)
		})
}

type glListPipelinesInput struct {
	Project string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	Ref     string `json:"ref,omitempty" description:"Filter by git ref (branch/tag)."`
}

func newGitLabListPipelinesTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_list_pipelines",
		"List recent CI pipelines for a project.",
		func(_ context.Context, in glListPipelinesInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			pipes, err := gitlabClient.ListPipelines(in.Project, in.Ref)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(pipes)
		})
}

type glPipelineInput struct {
	Project string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	ID      int64  `json:"id" description:"Pipeline ID."`
}

func newGitLabGetPipelineTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_get_pipeline",
		"Get details of a specific CI pipeline (status, ref, sha, web URL).",
		func(_ context.Context, in glPipelineInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			p, err := gitlabClient.GetPipeline(in.Project, in.ID)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(p)
		})
}

// ── Write tools (registered only when read-write) ───────────────────────────

type glCreateMRInput struct {
	Project      string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	Title        string `json:"title" description:"MR title."`
	Description  string `json:"description,omitempty" description:"MR description (Markdown)."`
	SourceBranch string `json:"source_branch" description:"Source branch with changes."`
	TargetBranch string `json:"target_branch" description:"Target branch to merge into (e.g. main)."`
}

func newGitLabCreateMRTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_create_mr",
		"Create a new merge request.",
		func(_ context.Context, in glCreateMRInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			mr, err := gitlabClient.CreateMergeRequest(in.Project, in.Title, in.Description, in.SourceBranch, in.TargetBranch)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(mr)
		})
}

type glUpdateMRInput struct {
	Project     string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	IID         int64  `json:"iid" description:"Merge request IID."`
	Title       string `json:"title,omitempty" description:"New MR title."`
	Description string `json:"description,omitempty" description:"New MR description (Markdown)."`
	Labels      string `json:"labels,omitempty" description:"Comma-separated labels to set."`
}

func newGitLabUpdateMRTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_update_mr",
		"Update a merge request (title, description, labels).",
		func(_ context.Context, in glUpdateMRInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			mr, err := gitlabClient.UpdateMergeRequest(in.Project, in.IID, in.Title, in.Description, in.Labels)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(mr)
		})
}

type glMRNoteInput struct {
	Project string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	IID     int64  `json:"iid" description:"Merge request IID."`
	Body    string `json:"body" description:"Note body (Markdown supported)."`
}

func newGitLabAddMRNoteTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_add_mr_note",
		"Add a comment (note) to a merge request.",
		func(_ context.Context, in glMRNoteInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			n, err := gitlabClient.AddMergeRequestNote(in.Project, in.IID, in.Body)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(n)
		})
}

type glIssueNoteInput struct {
	Project string `json:"project,omitempty" description:"Project path or ID. Omit to use bound project."`
	IID     int64  `json:"iid" description:"Issue IID."`
	Body    string `json:"body" description:"Note body (Markdown supported)."`
}

func newGitLabAddIssueNoteTool() fantasy.AgentTool {
	return fantasy.NewAgentTool("gitlab_add_issue_note",
		"Add a comment (note) to an issue.",
		func(_ context.Context, in glIssueNoteInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			n, err := gitlabClient.AddIssueNote(in.Project, in.IID, in.Body)
			if err != nil {
				return glErr(err)
			}
			return jsonResponse(n)
		})
}

// gitlabTools initializes the GitLab client from the environment and returns the
// native GitLab tools. Returns nil when GITLAB_TOKEN is not set. Write tools are
// included only when the client is read-write.
func gitlabTools() []fantasy.AgentTool {
	if !gitlab.IsConfigured() {
		return nil
	}
	c, err := gitlab.NewFromEnv()
	if err != nil {
		slog.Warn("gitlab tools disabled", "error", err)
		return nil
	}
	gitlabClient = c

	tools := []fantasy.AgentTool{
		newGitLabGetProjectTool(),
		newGitLabListGroupProjectsTool(),
		newGitLabSearchTool(),
		newGitLabListMRsTool(),
		newGitLabGetMRTool(),
		newGitLabGetMRDiffTool(),
		newGitLabListIssuesTool(),
		newGitLabGetIssueTool(),
		newGitLabListPipelinesTool(),
		newGitLabGetPipelineTool(),
	}
	if !c.ReadOnly() {
		tools = append(tools,
			newGitLabCreateMRTool(),
			newGitLabUpdateMRTool(),
			newGitLabAddMRNoteTool(),
			newGitLabAddIssueNoteTool(),
		)
	}
	slog.Info("native gitlab tools enabled",
		"count", len(tools), "readOnly", c.ReadOnly(),
		"group", c.Group(), "project", c.DefaultProject())
	return tools
}
