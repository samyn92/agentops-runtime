/*
Agent Runtime — Fantasy (Go)

Memory system: replaces session.go with a three-layer architecture.

 1. Working Memory — fixed-size sliding window of fantasy.Message in Go memory.
    Ephemeral: lost on pod restart. This is what gets passed to the Fantasy SDK.

 2. Short-term Memory — session summaries stored in Engram (auto-managed).
    Fetched via HTTP before each turn and prepended as context.

 3. Long-term Memory — explicit observations in Engram (user/agent-managed).
    Same Engram instance, searched on demand.

Engram is accessed via its REST API (not MCP). The runtime is a thin HTTP client.
*/
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
)

// ====================================================================
// Working Memory — bounded sliding window of messages
// ====================================================================

// WorkingMemory holds the last N messages in a sliding window.
// Thread-safe. Ephemeral — lost on pod restart by design.
type WorkingMemory struct {
	mu       sync.RWMutex
	messages []fantasy.Message
	maxSize  int
	turnNum  int // number of completed turns (user prompt + assistant response)
}

// NewWorkingMemory creates a working memory with the given window size.
func NewWorkingMemory(windowSize int) *WorkingMemory {
	if windowSize < 2 {
		windowSize = 20
	}
	return &WorkingMemory{
		messages: make([]fantasy.Message, 0, windowSize),
		maxSize:  windowSize,
	}
}

// Messages returns a copy of the current message window.
func (wm *WorkingMemory) Messages() []fantasy.Message {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	out := make([]fantasy.Message, len(wm.messages))
	copy(out, wm.messages)
	return out
}

// Append adds messages to the window, dropping the oldest if the window is full.
// Messages are trimmed from the front to stay within maxSize, ensuring we always
// keep the most recent messages. We trim at user-message boundaries to avoid
// orphaned tool-result messages.
func (wm *WorkingMemory) Append(msgs ...fantasy.Message) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wm.messages = append(wm.messages, msgs...)

	// Trim from front if over capacity
	if len(wm.messages) > wm.maxSize {
		excess := len(wm.messages) - wm.maxSize

		// Try to find a user-message boundary near the trim point so we
		// don't leave orphaned assistant/tool messages at the start.
		trimAt := excess
		for i := excess; i < len(wm.messages) && i < excess+5; i++ {
			if wm.messages[i].Role == fantasy.MessageRoleUser {
				trimAt = i
				break
			}
		}

		wm.messages = wm.messages[trimAt:]
	}
}

// CompleteTurn increments the turn counter. Called after each prompt/response cycle.
func (wm *WorkingMemory) CompleteTurn() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.turnNum++
}

// TurnCount returns the number of completed turns.
func (wm *WorkingMemory) TurnCount() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.turnNum
}

// MessageCount returns the current number of messages in the window.
func (wm *WorkingMemory) MessageCount() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return len(wm.messages)
}

// IsBusy and related state are tracked on daemonServer, not here.

// ====================================================================
// Engram Client — HTTP REST client for the shared memory server
// ====================================================================

// EngramClient talks to the Engram REST API for persistent memory operations.
type EngramClient struct {
	baseURL   string // e.g. "http://engram.agents.svc.cluster.local:7437"
	project   string // scoped to this agent (defaults to agent name)
	sessionID string // Engram session ID for this runtime lifecycle
	client    *http.Client
}

// NewEngramClient creates a client. Returns nil if serverURL is empty (memory disabled).
func NewEngramClient(serverURL, project string) *EngramClient {
	if serverURL == "" {
		return nil
	}
	return &EngramClient{
		baseURL:   serverURL,
		project:   project,
		sessionID: uuid.NewString(),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Init creates a session in Engram for this runtime lifecycle.
// Called once at daemon startup.
func (ec *EngramClient) Init() error {
	if ec == nil {
		return nil
	}

	body := map[string]string{
		"id":      ec.sessionID,
		"project": ec.project,
	}

	_, err := ec.post("/sessions", body)
	if err != nil {
		return fmt.Errorf("engram init session: %w", err)
	}

	slog.Info("engram session created", "session_id", ec.sessionID, "project", ec.project)
	return nil
}

// FetchContext retrieves recent context from Engram (short-term + long-term memories).
// Returns a formatted string suitable for prepending to the system context.
// Returns empty string on error or if no context is available.
func (ec *EngramClient) FetchContext(limit int) string {
	if ec == nil {
		return ""
	}

	params := url.Values{
		"project": {ec.project},
		"limit":   {strconv.Itoa(limit)},
	}

	resp, err := ec.get("/context", params)
	if err != nil {
		slog.Warn("engram fetch context failed", "error", err)
		return ""
	}

	// The /context endpoint returns a structured response with recent observations
	// and session summaries. Parse and format for injection.
	var contextResp struct {
		RecentObservations []struct {
			Type    string `json:"type"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"recent_observations"`
		RecentSessions []struct {
			Summary string `json:"summary"`
		} `json:"recent_sessions"`
	}

	if err := json.Unmarshal(resp, &contextResp); err != nil {
		slog.Warn("engram context parse failed", "error", err)
		return ""
	}

	var buf bytes.Buffer
	hasContent := false

	if len(contextResp.RecentSessions) > 0 {
		buf.WriteString("[Previous Session Context]\n")
		for _, s := range contextResp.RecentSessions {
			if s.Summary != "" {
				buf.WriteString("- ")
				buf.WriteString(s.Summary)
				buf.WriteString("\n")
				hasContent = true
			}
		}
		buf.WriteString("\n")
	}

	if len(contextResp.RecentObservations) > 0 {
		buf.WriteString("[Memory — Relevant Knowledge]\n")
		for _, o := range contextResp.RecentObservations {
			if o.Content != "" {
				buf.WriteString("- [")
				buf.WriteString(o.Type)
				buf.WriteString("] ")
				buf.WriteString(o.Title)
				buf.WriteString(": ")
				buf.WriteString(o.Content)
				buf.WriteString("\n")
				hasContent = true
			}
		}
		buf.WriteString("\n")
	}

	if !hasContent {
		return ""
	}
	return buf.String()
}

// SaveObservation explicitly saves an observation (long-term memory).
// Types: "decision", "discovery", "bugfix", "lesson", "procedure"
func (ec *EngramClient) SaveObservation(obsType, title, content string, tags []string) error {
	if ec == nil {
		return nil
	}

	body := map[string]any{
		"session_id": ec.sessionID,
		"type":       obsType,
		"title":      title,
		"content":    content,
	}
	if len(tags) > 0 {
		body["tags"] = tags
	}

	_, err := ec.post("/observations", body)
	if err != nil {
		return fmt.Errorf("engram save observation: %w", err)
	}

	slog.Info("engram observation saved", "type", obsType, "title", title)
	return nil
}

// PassiveCapture sends assistant output for Engram's passive extraction.
// Engram auto-detects noteworthy content (decisions, discoveries, etc.)
// and stores it. Fire-and-forget — errors are logged but not propagated.
func (ec *EngramClient) PassiveCapture(assistantOutput string) {
	if ec == nil || assistantOutput == "" {
		return
	}

	body := map[string]string{
		"session_id": ec.sessionID,
		"content":    assistantOutput,
	}

	go func() {
		_, err := ec.post("/observations/passive", body)
		if err != nil {
			slog.Debug("engram passive capture failed", "error", err)
		}
	}()
}

// EndSession ends the Engram session with an optional summary.
// Called on daemon shutdown.
func (ec *EngramClient) EndSession(summary string) {
	if ec == nil {
		return
	}

	body := map[string]string{}
	if summary != "" {
		body["summary"] = summary
	}

	_, err := ec.post("/sessions/"+ec.sessionID+"/end", body)
	if err != nil {
		slog.Warn("engram end session failed", "error", err)
	} else {
		slog.Info("engram session ended", "session_id", ec.sessionID)
	}
}

// Search performs a full-text search across memories.
func (ec *EngramClient) Search(query string, limit int) ([]EngramSearchResult, error) {
	if ec == nil {
		return nil, nil
	}

	params := url.Values{
		"q":       {query},
		"project": {ec.project},
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	resp, err := ec.get("/search", params)
	if err != nil {
		return nil, fmt.Errorf("engram search: %w", err)
	}

	var results []EngramSearchResult
	if err := json.Unmarshal(resp, &results); err != nil {
		return nil, fmt.Errorf("engram search parse: %w", err)
	}
	return results, nil
}

// EngramSearchResult represents a search hit from Engram.
type EngramSearchResult struct {
	ID      int     `json:"id"`
	Type    string  `json:"type"`
	Title   string  `json:"title"`
	Content string  `json:"content"`
	Rank    float64 `json:"rank"`
}

// ── HTTP helpers ──

func (ec *EngramClient) get(path string, params url.Values) ([]byte, error) {
	u := ec.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	resp, err := ec.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (ec *EngramClient) post(path string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	resp, err := ec.client.Post(ec.baseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
