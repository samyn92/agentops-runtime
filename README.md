# agentops-runtime

Standalone Go binary that powers AI agent pods in [AgentOps](https://github.com/samyn92/agentops-core). Built on the [Charm Fantasy SDK](https://github.com/charmbracelet/fantasy) with a three-layer memory system backed by [Engram](https://github.com/samyn92/engram), Kubernetes-native agent orchestration, MCP tool integration, and a streaming protocol (FEP) for the AgentOps console.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│  Agent Pod                                          │
│                                                     │
│  ┌───────────────────────────────────────────────┐  │
│  │  agentops-runtime (this binary)               │  │
│  │                                               │  │
│  │  Fantasy SDK Agent                            │  │
│  │    ├── Provider (Anthropic/OpenAI/Google/...)  │  │
│  │    ├── Built-in tools (bash, read, edit, ...)  │  │
│  │    ├── OCI tools (kubectl, helm, ...)          │  │
│  │    ├── Permission gate                        │  │
│  │    ├── Question gate                          │  │
│  │    └── Security hooks                         │  │
│  │                                               │  │
│  │  Memory                                       │  │
│  │    ├── Working memory (sliding window)        │  │
│  │    └── Engram client (short + long term)      │  │
│  │                                               │  │
│  │  HTTP Server (:4096)                          │  │
│  │    └── FEP SSE streaming                      │  │
│  └───────────────────────────────────────────────┘  │
│                                                     │
│  ┌─────────────┐  ┌─────────────────────────────┐   │
│  │ MCP Gateway  │  │ OCI Tool Sidecars           │   │
│  │ (optional)   │  │ (stdio MCP servers)         │   │
│  └─────────────┘  └─────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
         │                          │
         │ SSE/stdio                │ HTTP REST
         v                          v
   MCPServer CRs              Engram Service
   (e.g. kubernetes)     (shared memory server)
```

## Modes

### Daemon

Long-running HTTP server for `Deployment`-backed agents. Serves the FEP streaming protocol on port `4096`, maintains conversation state in working memory, persists knowledge to Engram.

```sh
agentops-runtime daemon
```

### Task

One-shot execution for `Job`-backed agents. Reads `AGENT_PROMPT` from env, runs the agent, writes JSON result to stdout.

```sh
agentops-runtime task
```

## Memory

Three-layer memory system that replaces the old unbounded session replay:

| Layer | Storage | Survives restart | Managed by |
|-------|---------|-----------------|------------|
| **Working** | Go memory (sliding window) | No | Automatic |
| **Short-term** | Engram SQLite (PVC) | Yes | Automatic (summaries, passive capture) |
| **Long-term** | Engram SQLite (PVC) | Yes | User + agent (explicit saves) |

**Working memory** keeps the last N messages (default 20) in a bounded sliding window. Trims at user-message boundaries to avoid orphaned tool results.

**Engram integration** provides persistent memory via HTTP REST:
- Context fetch at conversation start (recent session summaries + relevant knowledge)
- Passive capture of assistant output (Engram auto-extracts noteworthy content)
- Explicit observation saves for important decisions and discoveries
- FTS5 search across all agent memories

Configure via the Agent CRD:

```yaml
spec:
  memory:
    serverRef: engram        # Service name in the agents namespace
    project: my-agent        # Memory scope (defaults to agent name)
    contextLimit: 5          # Max context items to fetch
    windowSize: 20           # Working memory message limit
    autoSummarize: true      # Auto-capture session summaries
```

## API

All routes are prefixed at the root. The streaming endpoint speaks FEP (Fantasy Event Protocol) over SSE.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/prompt` | Non-streaming prompt, returns JSON |
| `POST` | `/prompt/stream` | Streaming prompt, returns FEP SSE events |
| `POST` | `/steer` | Inject a steering message mid-execution |
| `DELETE` | `/abort` | Cancel the running generation |
| `POST` | `/permission/{pid}/reply` | Reply to a permission gate (`once` / `always` / `deny`) |
| `POST` | `/question/{qid}/reply` | Reply to an agent question |
| `GET` | `/healthz` | Kubernetes health probe |
| `GET` | `/status` | Runtime status (model, steps, busy, memory) |

### FEP Events

The `/prompt/stream` endpoint emits these SSE events:

```
agent_start, agent_finish, agent_error
step_start, step_finish
text_start, text_delta, text_end
reasoning_start, reasoning_delta, reasoning_end
tool_input_start, tool_input_delta, tool_input_end
tool_call, tool_result
source, warnings
stream_finish
permission_asked, question_asked
session_idle
```

## Built-in Tools

Eight tools available out of the box, selectable via `spec.builtinTools`:

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands |
| `read` | Read file contents with optional line range |
| `edit` | Exact find-and-replace text editing |
| `write` | Write/create files |
| `grep` | Regex search with ripgrep |
| `ls` | List directory contents |
| `glob` | Find files by glob pattern |
| `fetch` | Fetch URL content via curl |

All tools emit `ui` metadata hints so the console can render rich cards (terminal output, code blocks, diffs, file trees, search results).

## Tool Security

### Permission Gates

Tools listed in `spec.permissionTools` require user approval before execution. The runtime emits a `permission_asked` FEP event and blocks until the user replies via `/permission/{pid}/reply`.

Responses: `once` (allow this call), `always` (permanently allow this tool), `deny` (block).

### Hooks

`spec.toolHooks` provides three layers of protection:

- **blockedCommands** -- reject bash commands containing these patterns
- **allowedPaths** -- restrict file tools to these path prefixes
- **auditTools** -- log execution of these tools for audit trails

## MCP Integration

Two types of MCP tool sources:

**OCI Tools** (`spec.toolRefs`) -- Tool packages pulled as OCI artifacts, started as stdio MCP servers inside the pod. Each tool directory contains a `manifest.json` and a binary.

**Gateway MCP** (`spec.mcpServers`) -- Shared MCPServer CRs (like a Kubernetes MCP server) accessed through the MCP gateway sidecar via SSE.

## Providers

Supports any LLM provider through the Fantasy SDK:

| Provider | Env var |
|----------|---------|
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai` | `OPENAI_API_KEY` |
| `google` / `gemini` | `GOOGLE_API_KEY` |
| `openrouter` | `OPENROUTER_API_KEY` |
| Custom (OpenAI-compatible) | `<NAME>_API_KEY` + `<NAME>_BASE_URL` |

Fallback models are tried automatically on retryable errors (429, 5xx, rate limits).

## Agent Orchestration

Two built-in orchestration tools allow agents to delegate work:

- **`run_agent`** -- Creates an `AgentRun` CR to trigger another agent with a prompt
- **`get_agent_run`** -- Checks the status and output of a running agent

Requires in-cluster Kubernetes access (automatic in agent pods).

## Configuration

The runtime reads `/etc/operator/config.json`, generated by the AgentOps operator from the Agent CRD spec. The operator handles all config assembly -- providers, tools, MCP bindings, memory settings, security hooks, and resource bindings.

```json
{
  "runtime": "fantasy",
  "primaryModel": "anthropic/claude-sonnet-4-20250514",
  "providers": [{"name": "anthropic"}],
  "builtinTools": ["bash", "read", "edit", "write", "grep", "ls", "glob"],
  "memory": {
    "serverURL": "http://engram.agents.svc:7437",
    "project": "my-agent",
    "contextLimit": 5,
    "windowSize": 20,
    "autoSummarize": true
  }
}
```

## Container Image

```
ghcr.io/samyn92/agentops-runtime:<version>
ghcr.io/samyn92/agentops-runtime:latest
```

Based on `alpine:3.21` with `bash`, `curl`, and `ripgrep` for the built-in tools.

## Building

```sh
CGO_ENABLED=0 go build -o agentops-runtime .
```

## Related

- [agentops-core](https://github.com/samyn92/agentops-core) -- Kubernetes operator
- [agentops-console](https://github.com/samyn92/agentops-console) -- Web console (Go BFF + SolidJS PWA)
- [Engram](https://github.com/samyn92/engram) -- Shared memory server (fork)
- [Charm Fantasy SDK](https://github.com/charmbracelet/fantasy) -- AI agent framework

## License

Apache 2.0
