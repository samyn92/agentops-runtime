/*
Agent Runtime — Fantasy (Go)

Kubernetes client for creating and querying AgentRun CRs.
Used by the run_agent and get_agent_run orchestration tools.
Runs in-cluster using the pod's service account.
*/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	apiGroup       = "agents.agentops.io"
	apiVersion     = "v1alpha1"
	agentRunPlural = "agentruns"
	agentPlural    = "agents"
)

var agentRunGVR = schema.GroupVersionResource{
	Group:    apiGroup,
	Version:  apiVersion,
	Resource: agentRunPlural,
}

var agentGVR = schema.GroupVersionResource{
	Group:    apiGroup,
	Version:  apiVersion,
	Resource: agentPlural,
}

// K8sClient provides operations for AgentRun CRs.
type K8sClient struct {
	client    dynamic.Interface
	namespace string
}

// NewK8sClient creates a new client using in-cluster config.
func NewK8sClient() (*K8sClient, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}

	ns := os.Getenv("AGENT_NAMESPACE")
	if ns == "" {
		ns = "default"
	}

	return &K8sClient{
		client:    dynClient,
		namespace: ns,
	}, nil
}

// AgentInfo holds basic info about an Agent CR.
type AgentInfo struct {
	Name  string `json:"name"`
	Mode  string `json:"mode"`
	Phase string `json:"phase"`
}

// GetAgent checks if an Agent CR exists and returns basic info.
func (k *K8sClient) GetAgent(ctx context.Context, name string) (*AgentInfo, error) {
	obj, err := k.client.Resource(agentGVR).Namespace(k.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	mode, _, _ := unstructured.NestedString(obj.Object, "spec", "mode")
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")

	return &AgentInfo{Name: name, Mode: mode, Phase: phase}, nil
}

// ListAgents returns all Agent CRs in the namespace.
func (k *K8sClient) ListAgents(ctx context.Context) ([]AgentInfo, error) {
	list, err := k.client.Resource(agentGVR).Namespace(k.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	agents := make([]AgentInfo, 0, len(list.Items))
	for _, item := range list.Items {
		name := item.GetName()
		mode, _, _ := unstructured.NestedString(item.Object, "spec", "mode")
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		agents = append(agents, AgentInfo{Name: name, Mode: mode, Phase: phase})
	}
	return agents, nil
}

// AgentRunResult holds the result of creating an AgentRun.
type AgentRunResult struct {
	Name string `json:"name"`
}

// AgentRunGitParams holds optional git workspace params for an AgentRun.
type AgentRunGitParams struct {
	ResourceRef string `json:"resourceRef"`
	Branch      string `json:"branch"`
	BaseBranch  string `json:"baseBranch,omitempty"`
}

// AgentRunStatus holds the status of an AgentRun.
type AgentRunStatus struct {
	Phase          string `json:"phase"`
	Output         string `json:"output"`
	ToolCalls      int64  `json:"toolCalls"`
	Model          string `json:"model"`
	PullRequestURL string `json:"pullRequestURL,omitempty"`
	Commits        int64  `json:"commits,omitempty"`
	Branch         string `json:"branch,omitempty"`
}

// CreateAgentRun creates an AgentRun CR. If gitParams is non-nil, spec.git is populated.
func (k *K8sClient) CreateAgentRun(ctx context.Context, agentRef, prompt, source, sourceRef string, gitParams *AgentRunGitParams) (*AgentRunResult, error) {
	name := fmt.Sprintf("%s-run-%d", agentRef, time.Now().UnixMilli())

	spec := map[string]interface{}{
		"agentRef":  agentRef,
		"prompt":    prompt,
		"source":    source,
		"sourceRef": sourceRef,
	}

	if gitParams != nil {
		gitMap := map[string]interface{}{
			"resourceRef": gitParams.ResourceRef,
			"branch":      gitParams.Branch,
		}
		if gitParams.BaseBranch != "" {
			gitMap["baseBranch"] = gitParams.BaseBranch
		}
		spec["git"] = gitMap
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": fmt.Sprintf("%s/%s", apiGroup, apiVersion),
			"kind":       "AgentRun",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": k.namespace,
				"labels": map[string]interface{}{
					"agents.agentops.io/agent": agentRef,
				},
			},
			"spec": spec,
		},
	}

	_, err := k.client.Resource(agentRunGVR).Namespace(k.namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create AgentRun: %w", err)
	}

	return &AgentRunResult{Name: name}, nil
}

// GetAgentRun retrieves an AgentRun CR and returns its status.
func (k *K8sClient) GetAgentRun(ctx context.Context, name string) (*AgentRunStatus, error) {
	obj, err := k.client.Resource(agentRunGVR).Namespace(k.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get AgentRun %s: %w", name, err)
	}

	status, found, err := unstructured.NestedMap(obj.Object, "status")
	if err != nil || !found {
		return &AgentRunStatus{Phase: "Unknown"}, nil
	}

	// Marshal and unmarshal for clean extraction
	data, _ := json.Marshal(status)
	var result AgentRunStatus
	json.Unmarshal(data, &result)

	return &result, nil
}
