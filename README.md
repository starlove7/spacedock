<div align="center">

English | [한국어](./README.ko.md)

<img src="./assets/spacedock-icon.svg" alt="SpaceDock logo" width="128" />

# SpaceDock

</div>

SpaceDock is a **self-hosted MCP development runtime** for controlling remote development servers from ChatGPT. It combines DevSpace-style Allowed Root/Workspace boundaries with structured Go-native development tools, and includes remote OAuth, persistent systemd operation, managed Git worktrees, real ACP prompt/turn handling, and bounded subagents from the initial release.

Subagents are not forced through a single protocol:

- **Codex** uses the installed `codex` CLI directly through its `app-server`. No `codex-acp` adapter is required.
- **GitHub Copilot** uses the ACP provider, typically by launching `copilot --acp`.

## Install

End users do not need Go. Node.js 18+ and npm are enough to install the prebuilt Go binary.

```sh
npm install -g @starlove7/spacedock
spacedock version
```

Supported targets are Linux, macOS, and Windows on x64/amd64 and arm64.

## Local usage

For a local MCP client, the recommended setup is:

```sh
npm install -g @starlove7/spacedock
spacedock init --root /home/you/src --root-id src --root-name "Local source"
spacedock serve --stdio
```

`spacedock init` creates the minimal local configuration and a valid owner token file. A manual minimal configuration must still use the parser's structure, for example:

```yaml
state_dir: ~/.spacedock
server:
  oauth:
    owner_token_file: ~/.spacedock/oauth-owner.token
allowed_roots:
  - id: src
    path: /home/you/src
    permissions: [fs.read, fs.write, workspace.manage]
```

This is the minimum permission set for the filesystem tools. Add `command.execute` to the Allowed Root to use `exec_command`, and add `agent.execute` to use agent tools.

After initialization, manage additional Allowed Roots without editing YAML directly:

```sh
spacedock root add --path /home/you/other-project --id other --name "Other project"
spacedock root list
spacedock root remove other
```

Each command accepts `--config <path>`; when omitted, SpaceDock uses `~/.spacedock/config.yaml`. A root added without an explicit ID or name derives them from the path. `root add` grants the same nine default permissions as `spacedock init`: `fs.read`, `fs.write`, `command.execute`, `git.read`, `workspace.manage`, `recall.read`, `recall.write`, `acp.connect`, and `agent.execute`. Changes are read when the next `serve` process or service restart starts; a running server is not hot-reloaded.

`Config.Load` validates `owner_token_file` even for stdio, although stdio does not use the HTTP OAuth middleware. The token file itself must therefore exist and contain a valid token; using `init` is recommended because it creates it.

The transport information guaranteed by SpaceDock for an stdio MCP client is:

```text
command: spacedock
args: serve --stdio
transport: stdio
```

With a custom configuration, add `--config <path>` to the arguments. This describes the transport only and does not assume a particular client's JSON schema. Local HTTP is also supported with `spacedock serve`: it listens on loopback and serves `/mcp`, with the OAuth middleware applied. Prefer stdio for local clients that do not support OAuth.

### Connect local stdio SpaceDock to ChatGPT

ChatGPT cannot attach directly to a local process's stdio pipe by registering an MCP URL such as `https://spacedock.example.com/mcp`. To keep SpaceDock local while using it from ChatGPT, use [OpenAI Secure MCP Tunnel](https://github.com/openai/tunnel-client). `tunnel-client` keeps an outbound connection to OpenAI and launches SpaceDock locally as its stdio MCP child process; SpaceDock itself does not need a public inbound endpoint.

1. Create or select a tunnel in [OpenAI Platform Tunnels](https://platform.openai.com/settings/organization/tunnels). The tunnel must be scoped so it is available to the ChatGPT workspace that will use it.
2. Install a supported `tunnel-client`, then create a restricted Runtime API key with **Tunnels Read + Use** permission. Keep that key in `CONTROL_PLANE_API_KEY`; do not put the secret directly in the MCP command or commit it to the repository.
3. Create a local stdio profile for SpaceDock:

```sh
export CONTROL_PLANE_API_KEY="sk-..."

tunnel-client init \
  --sample sample_mcp_stdio_local \
  --profile spacedock-local \
  --tunnel-id tunnel_0123456789abcdef0123456789abcdef \
  --mcp-command "spacedock serve --stdio"

tunnel-client doctor --profile spacedock-local --explain
tunnel-client run --profile spacedock-local
```

If SpaceDock uses a non-default config, set the command to `spacedock serve --stdio --config /absolute/path/to/config.yaml` instead. Do **not** start a separate `spacedock serve --stdio` process for this profile: `tunnel-client` owns the stdio pipes and starts SpaceDock itself.

4. While `tunnel-client run --profile spacedock-local` is healthy and running, open [ChatGPT connector settings](https://chatgpt.com/#settings/Connectors), choose **Connection: Tunnel**, and select the same tunnel or paste its `tunnel_id`. Keep the tunnel runtime running for connector discovery and later MCP calls.

The resulting path is:

```text
ChatGPT
  ↕ Secure MCP Tunnel
OpenAI tunnel control plane
  ↕ outbound HTTPS
local tunnel-client
  ↕ stdio
spacedock serve --stdio
```

This is different from remote HTTPS deployment: a public SpaceDock endpoint such as `https://spacedock.example.com/mcp` is registered as a URL, while local stdio SpaceDock is exposed to ChatGPT through the tunnel object rather than through a public URL.

Local Codex use requires the `codex` CLI to be installed, available in the execution environment, and authenticated there. `agents.codex.command` may be a PATH name or an executable path; SpaceDock launches Codex's app-server. For example:

```yaml
agents:
  codex:
    command: codex
  profiles:
    - id: local-codex
      provider: codex
      model: gpt-5.6-luna
      effort: medium
      write_mode: allowed
```

Local Copilot ACP use requires an executable endpoint command. The parser requires an absolute executable path, with `args: [--acp]`; the profile uses `provider: acp` and the endpoint's `endpoint_id`:

```yaml
acp:
  endpoints:
    - id: copilot
      command: /absolute/path/to/copilot
      args: [--acp]
      env_from: {}
agents:
  profiles:
    - id: local-copilot
      provider: acp
      endpoint_id: copilot
```

The local agent flow is `agent_run(workspace_id, profile_id, prompt)` → `agent_show(workspace_id, agent_id[, wait_ms])` →, when needed, `agent_continue(workspace_id, agent_id, prompt)` → `agent_show` again → `agent_stop(workspace_id, agent_id)` when cancellation or shutdown is needed. `agent_continue` reuses the same provider session: the same Codex thread or the same ACP remote session. `agent_stop` cancels the running turn and closes the provider session. Agent tools require the Allowed Root's `agent.execute` permission.

All local modes still apply Allowed Root and Workspace permissions. The structured filesystem tools follow `workspace_list` → `workspace_open` → workspace-scoped `read_file`, `list_dir`, `list_files`, `search_text`, and `file_edit`. For commands, use `exec_command`; when it returns a running session, use `session_observe` to inspect it and `session_act` to interact with it.

Choose `checkout` when you intentionally want to modify the current checkout directly. Choose a managed `worktree` for an isolated detached task; a dirty managed worktree is not closed normally and requires an intentional discard action.

The structured filesystem tools always deny built-in sensitive components such as `.ssh`, `.aws`, `.gnupg`, `.env`/`.env.*`, credentials files, SSH keys and `known_hosts`, and `.pem`, `.key`, `.pfx`, or `.p12` files. `security.sensitive_paths.additional_patterns` adds component globs; the built-in protection cannot be disabled by configuration. Direct access returns `PERMISSION_DENIED`, while broad listings and searches hide sensitive entries and do not count them. This layer applies only to structured filesystem tools. `command.execute` is not an OS sandbox: shell subprocesses run with the SpaceDock process's OS-user permissions, and this sensitive-path layer does not block arbitrary shell commands.

| Mode | Transport | OAuth | Typical use |
| --- | --- | --- | --- |
| Local stdio | `spacedock serve --stdio` | HTTP OAuth middleware not used | Local MCP clients directly; ChatGPT through Secure MCP Tunnel |
| Local loopback HTTP | `spacedock serve`, `/mcp` | Applied | Local HTTP clients that support OAuth |
| Remote HTTPS | HTTPS reverse proxy/tunnel → loopback SpaceDock | Applied | ChatGPT/remote operation; operator-managed proxy/tunnel and usually systemd |

## Quick start for remote ChatGPT use

For example, to allow projects below `/home/ubuntu/github` on an OCI host:

```sh
spacedock init \
  --root /home/ubuntu/github \
  --root-id github \
  --root-name "GitHub Projects" \
  --public-base-url https://spacedock.example.com
```

The default config is `~/.spacedock/config.yaml`; the owner approval token is `~/.spacedock/oauth-owner.token`. The token contents are never printed by `init`.

Then configure agent providers/profiles in `config.yaml`. See `config.example.yaml` for a complete example.

## Codex CLI provider

Codex uses the **Codex CLI itself**, not an ACP adapter.

```yaml
agents:
  max_concurrent: 4
  codex:
    command: codex
  profiles:
    - id: codex1
      name: codex1
      description: Codex worker that implements an approved patch specification
      provider: codex
      instructions: Implement only the supplied patch specification.
      model: gpt-5.6-luna
      effort: medium
      write_mode: allowed
```

SpaceDock creates a Codex provider session with this lifecycle:

```text
codex app-server
    ↓
initialize / initialized
    ↓
first turn: thread/start
later turn: thread/resume
    ↓
turn/start
    ↓
turn/completed
```

`agent_continue` reuses the same Codex thread ID through `thread/resume`, preserving provider conversation context. Profile `instructions` are prepended only to the first `agent_run`; they are not re-applied on `agent_continue`.

`write_mode` maps to the Codex sandbox:

```text
read_only   -> read-only / readOnly
allowed     -> workspace-write / workspaceWrite(networkAccess=true)
full_access -> danger-full-access / dangerFullAccess
```

SpaceDock uses `approvalPolicy=never` for Codex worker turns so a non-interactive subagent cannot stall waiting for an approval prompt. Choose the Workspace permissions and `write_mode` up front according to the worker's required authority.

`agents.codex.command` defaults to `codex` and is resolved from `PATH`. A systemd user service may have a different PATH from your login shell. If Codex was installed through nvm or another shell-specific environment, an absolute path is safer:

```yaml
agents:
  codex:
    command: /home/ubuntu/.nvm/versions/node/v22.23.2/bin/codex
```

## Copilot ACP provider

GitHub Copilot is connected through ACP. ACP endpoint `command` must be an **absolute executable path**.

```yaml
agents:
  profiles:
    - id: copilot1
      name: copilot1
      description: Copilot ACP worker
      provider: acp
      endpoint_id: copilot
      instructions: Implement only the supplied patch specification.
      permission_policy: manual
      config_options: {}

acp:
  endpoints:
    - id: copilot
      name: GitHub Copilot ACP
      command: /absolute/path/to/copilot
      args: [--acp]
      env_from: {}
```

Even if `copilot` is available in your interactive PATH, ACP endpoints intentionally use explicit absolute paths. Use `which copilot` (or the platform equivalent) to locate the executable.

For `provider: acp`, use `endpoint_id`, `permission_policy`, `mode_id`, and `config_options`. `model`, `effort`, and `write_mode` are Codex-provider fields.

## Persistent systemd operation

The primary Linux deployment is a user systemd service.

```sh
spacedock service install
spacedock service status
```

Other service commands:

```sh
spacedock service start
spacedock service stop
spacedock service restart
spacedock service status
spacedock service uninstall
```

`service install` writes `~/.config/systemd/user/spacedock.service` using the current SpaceDock executable and an absolute config path, then runs `systemctl --user enable --now spacedock.service`.

The SpaceDock HTTP listener is intentionally loopback-only (`127.0.0.1`, `::1`, or `localhost`). A user-managed HTTPS reverse proxy or tunnel must forward the public address to `127.0.0.1:8766`.

```text
ChatGPT
   │ HTTPS + OAuth MCP
   ▼
Reverse Proxy / Tunnel
   │
   ▼
127.0.0.1:8766
   │
   ▼
spacedock serve (systemd --user)
```

SpaceDock does not configure Cloudflare Tunnel/ngrok, tunnel credentials, `sudo`, or `loginctl enable-linger`. If the user service must remain active after logout, enabling linger is an operator-managed step.

## ChatGPT OAuth connection

Register this MCP URL in ChatGPT:

```text
https://spacedock.example.com/mcp
```

Remote HTTP MCP uses OAuth access tokens, not a static bearer token. SpaceDock provides:

- Protected Resource Metadata
- Authorization Server Metadata
- Dynamic Client Registration compatibility
- Authorization Code + PKCE S256
- owner-token approval page
- access and refresh tokens
- refresh-token rotation
- scope/resource validation
- RFC 9207 authorization-response `iss`

When the authorization page appears, enter the contents of `~/.spacedock/oauth-owner.token`. This value is an **owner approval secret**, not the client's OAuth access token.

Enable `trust_proxy: true` only behind a trusted reverse proxy that sets `X-Forwarded-For` correctly; it affects the client IP used for OAuth rate limiting.

## Allowed Roots and Workspaces

An Allowed Root is an **authorization boundary**, not one project. For example:

```yaml
allowed_roots:
  - id: github
    name: GitHub Projects
    path: /home/ubuntu/github
    permissions:
      - fs.read
      - fs.write
      - command.execute
      - git.read
      - workspace.manage
      - recall.read
      - recall.write
      - acp.connect
      - agent.execute
```

Typical ChatGPT flow:

```text
workspace_list
    ↓
workspace_open(root_id="github", path="spacedock", mode="checkout")
    ↓
ws_... workspace ID
    ↓
workspace-scoped tools
```

`path` is relative to the Allowed Root. Absolute paths, `..` escapes, URI/UNC/volume paths, and symlink escapes are rejected.

### checkout

Use the existing checkout directly:

```text
workspace_open(root_id="github", path="spacedock", mode="checkout")
```

### worktree

For isolated agent work, open a managed Git worktree:

```text
workspace_open(
  root_id="github",
  path="spacedock",
  mode="worktree",
  base_ref="HEAD"
)
```

Managed worktrees are detached and created below `<state_dir>/worktrees/<workspace-id>`. SpaceDock does not silently widen the boundary to a parent Git repository.

A dirty managed worktree is not removed by normal `workspace_close`; SpaceDock returns `WORKTREE_DIRTY`. Use `workspace_discard(force=true)` only when you intentionally want to discard it.

Workspace and managed-worktree metadata are persisted in `workspaces.json` and restored after a SpaceDock/systemd restart. Codex app-server processes, ACP processes, and agent execution sessions are process-local and are not restored.

## Generic ACP tools

ACP endpoints can also be used directly, independently of Copilot subagent profiles:

```text
acp_list
acp_connect
acp_capabilities
acp_prompt
acp_events
acp_cancel
acp_interactions
acp_respond
acp_disconnect
```

`acp_connect` performs a real `session/new` after initialization. `acp_prompt` runs `session/prompt` on the same remote session and collects `session/update` notifications into a bounded event ring. Exact `agent_thought_chunk` events are filtered before storage/exposure.

`permission_policy` is `manual` or `allow_once`. In manual mode, permission requests are exposed through `acp_interactions` and answered through `acp_respond`; permanent/`always` options are not automatically exposed or selected.

## Agent tools

The high-level Agent API is provider-agnostic:

```text
agent_list
agent_run
agent_show
agent_continue
agent_stop
```

- `agent_run` resolves the profile and starts either a Codex CLI or ACP provider turn.
- `agent_show` returns a generic run state and final response regardless of provider.
- `agent_continue` reuses the same provider session: same Codex thread or same ACP remote session.
- `agent_stop` cancels the running turn and closes the provider session.
- `agents.max_concurrent` limits simultaneously running **turns**, not total agent processes.

Agent records include `provider` and `provider_session_id`. For Codex, `provider_session_id` is the Codex thread ID. For ACP providers it is SpaceDock's local ACP session ID.

## Tool groups

SpaceDock exposes structured MCP tools instead of putting every operation behind one shell tool.

```text
Workspace: workspace_list, workspace_open, workspace_close, workspace_discard
Files:     read_file, list_dir, list_files, search_text, file_edit
Command:   exec_command, session_observe, session_act
Git:       git_status, git_diff, git_log
Recall:    recall_search, recall_read, recall_write, recall_delete
ACP:       acp_list, acp_connect, acp_capabilities, acp_prompt, acp_events,
           acp_cancel, acp_interactions, acp_respond, acp_disconnect
Agent:     agent_list, agent_run, agent_show, agent_continue, agent_stop
```

File/Git paths and command working directories are scoped to an opened Workspace.

## Security boundaries

`fs.*` operations and the Workspace path resolver enforce Allowed Root containment. `command.execute`, however, is **not an OS sandbox**. Spawned subprocesses have the authority of the local OS user that runs SpaceDock.

The sensitive-path deny layer applies only to structured filesystem tools. It always denies `.ssh`, `.aws`, `.gnupg`, `.env` and `.env.*`, credentials and SSH key/`known_hosts` names, and `.pem`, `.key`, `.pfx`, and `.p12` extensions. Additional component globs can be configured under `security.sensitive_paths.additional_patterns`; the built-in list cannot be disabled. Direct access is returned as structured `PERMISSION_DENIED`; broad listing/search omits sensitive entries. This layer does not restrict `command.execute` or arbitrary shell commands.

Codex `write_mode` is an additional Codex-provider execution policy; it does not replace SpaceDock's Allowed Root/permission model. ACP permission requests are also a separate layer from SpaceDock's `agent.execute`/`acp.connect` permissions.

Recommended practices:

- register only trusted development directories as Allowed Roots
- omit `command.execute`, `fs.write`, and `agent.execute` where they are not needed
- use the minimum necessary Codex `write_mode`
- keep the SpaceDock HTTP listener on loopback
- expose it externally only through HTTPS reverse proxy/tunnel infrastructure
- protect the owner token and OAuth state directory
- enable `trust_proxy` only behind a trusted proxy

## stdio mode

HTTP + OAuth + systemd remains the remote ChatGPT deployment. `stdio` is the local MCP transport; SpaceDock guarantees `command: spacedock`, `args: serve --stdio`, and `transport: stdio` (add `--config <path>` for a custom config). It does not prescribe a client-specific JSON schema, and HTTP OAuth middleware is not used in stdio mode.

```sh
spacedock serve --stdio
```

## Build and verify from source

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/spacedock
npm run build:npm-binaries
```

The Go module path is `github.com/starlove7/spacedock`.
