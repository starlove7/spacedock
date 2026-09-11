<div align="center">

[English](./README.md) | 한국어

<img src="./assets/spacedock-icon.svg" alt="SpaceDock logo" width="128" />

# SpaceDock

</div>

SpaceDock은 ChatGPT에서 원격 개발 서버를 직접 다루기 위한 **self-hosted MCP 개발 런타임**입니다. DevSpace의 Allowed Root/Workspace 접근 경계와 AgentDock 계열의 구조화된 Go-native 도구 개념을 결합하고, 초기 버전부터 OAuth 원격 연결, systemd 상시 실행, Git worktree, ACP 실제 prompt/turn, bounded subagent 실행을 제공합니다.

Subagent provider는 하나의 protocol로 강제하지 않습니다.

- **Codex**: 설치된 `codex` CLI의 `app-server`를 SpaceDock이 직접 실행합니다. `codex-acp`는 필요하지 않습니다.
- **GitHub Copilot**: ACP provider를 사용하며 일반적인 endpoint는 `copilot --acp`입니다.

## 설치

최종 사용자는 Go가 필요하지 않습니다. Node.js 18 이상과 npm만 있으면 사전 빌드된 Go 바이너리가 설치됩니다.

```sh
npm install -g @starlove7/spacedock
spacedock version
```

지원 플랫폼은 Linux/macOS/Windows의 x64(amd64), arm64입니다.

## 로컬 사용

로컬 MCP client에는 다음 초기화를 권장합니다.

```sh
npm install -g @starlove7/spacedock
spacedock init --root /home/you/src --root-id src --root-name "Local source"
spacedock serve --stdio
```

`spacedock init`은 로컬용 최소 설정과 유효한 owner token 파일을 만듭니다. 수동 최소 설정도 parser가 사용하는 구조를 따라야 합니다.

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

이 권한은 filesystem tool을 위한 최소 권한입니다. `exec_command`를 사용하려면 Allowed Root에 `command.execute`를 추가하고, agent tool을 사용하려면 `agent.execute`를 추가해야 합니다.

`Config.Load`는 stdio가 HTTP OAuth middleware를 사용하지 않더라도 `owner_token_file`을 검증합니다. 따라서 token file 자체가 존재하고 유효한 token을 포함해야 하며, `init`이 이를 만들어 주므로 권장합니다.

stdio MCP client에 대해 SpaceDock이 보장하는 transport 정보는 다음뿐입니다.

```text
command: spacedock
args: serve --stdio
transport: stdio
```

사용자 설정을 쓰면 args에 `--config <path>`를 추가합니다. 특정 client의 JSON schema를 전제한 설명은 아닙니다. 로컬 HTTP도 `spacedock serve`로 지원하며 loopback listener의 `/mcp` 경로에 OAuth middleware가 적용됩니다. OAuth를 지원하지 않는 로컬 client에는 stdio를 권장합니다.

로컬 Codex 사용 조건은 실행 환경에 `codex` CLI가 설치되어 있고 PATH에서 사용 가능하며 인증되어 있는 것입니다. `agents.codex.command`에는 PATH 이름이나 실행 파일 경로를 지정할 수 있고 SpaceDock이 Codex app-server를 실행합니다.

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

로컬 Copilot ACP는 실행 파일 endpoint가 필요합니다. parser상 `command`는 절대 실행 파일 경로여야 하고 `args: [--acp]`를 사용하며, profile은 `provider: acp`와 endpoint의 `endpoint_id`를 지정합니다.

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

로컬 agent 기본 흐름은 `agent_run(workspace_id, profile_id, prompt)` → `agent_show(workspace_id, agent_id[, wait_ms])` → 필요 시 `agent_continue(workspace_id, agent_id, prompt)` → 다시 `agent_show` → 취소 또는 종료가 필요할 때 `agent_stop(workspace_id, agent_id)`입니다. `agent_continue`는 동일 provider session을 재사용하며 Codex는 같은 thread, ACP는 같은 remote session을 사용합니다. `agent_stop`은 running turn을 취소하고 provider session을 닫습니다. Agent tool을 사용하려면 Allowed Root에 `agent.execute` 권한이 필요합니다.

로컬에서도 Allowed Root와 Workspace 권한이 적용됩니다. 구조화된 filesystem 흐름은 `workspace_list` → `workspace_open` → workspace-scoped `read_file`, `list_dir`, `list_files`, `search_text`, `file_edit`입니다. 명령은 `exec_command`를 사용하고, running session을 반환하면 `session_observe`로 관찰하고 `session_act`로 상호작용합니다.

현재 checkout을 직접 수정할 때는 `checkout`을 선택하고, 격리된 detached 작업에는 managed `worktree`를 선택합니다. dirty managed worktree는 일반 close로 닫히지 않으며 의도적인 discard가 필요합니다.

구조화된 filesystem tool은 `.ssh`, `.aws`, `.gnupg`, `.env`/`.env.*`, credentials 계열, SSH key/`known_hosts`, `.pem`, `.key`, `.pfx`, `.p12` 같은 기본 민감 경로를 항상 차단합니다. `security.sensitive_paths.additional_patterns`는 component glob을 추가하며 기본 보호는 설정으로 해제할 수 없습니다. 직접 접근은 `PERMISSION_DENIED`를 반환하고 broad listing/search에서는 민감 항목을 숨기며 개수에도 포함하지 않습니다. 이 계층은 구조화된 filesystem tool에만 적용됩니다. `command.execute`는 OS sandbox가 아니므로 shell subprocess는 SpaceDock 실행 OS 사용자의 권한을 가지며 이 민감 경로 계층이 임의 shell command를 차단하지 않습니다.

| 모드 | Transport | OAuth | 용도 |
| --- | --- | --- | --- |
| 로컬 stdio | `spacedock serve --stdio` | HTTP OAuth middleware 미사용 | 로컬 MCP client; OAuth 미지원 client에 권장 |
| 로컬 loopback HTTP | `spacedock serve`, `/mcp` | 적용됨 | OAuth를 지원하는 로컬 HTTP client |
| 원격 HTTPS | HTTPS reverse proxy/tunnel → loopback SpaceDock | 적용됨 | ChatGPT/원격 운영; operator-managed proxy/tunnel 및 보통 systemd |

## 원격 ChatGPT용 빠른 시작

예를 들어 OCI 서버에서 `/home/ubuntu/github` 아래 프로젝트들을 ChatGPT에 허용하려면 다음과 같이 초기화합니다.

```sh
spacedock init \
  --root /home/ubuntu/github \
  --root-id github \
  --root-name "GitHub Projects" \
  --public-base-url https://spacedock.example.com
```

기본 설정 위치는 `~/.spacedock/config.yaml`, owner token은 `~/.spacedock/oauth-owner.token`입니다. Owner token 내용은 초기화 출력에 노출되지 않습니다.

그 다음 `config.yaml`에 agent provider/profile을 설정합니다. `config.example.yaml`을 참고할 수 있습니다.

## Codex CLI provider

Codex는 ACP adapter가 아니라 **Codex CLI 자체**를 provider로 사용합니다.

```yaml
agents:
  max_concurrent: 4
  codex:
    command: codex
  profiles:
    - id: codex1
      name: 코덱스1호
      description: 확정된 명세를 구현하는 Codex worker
      provider: codex
      instructions: 전달받은 패치 명세만 구현한다.
      model: gpt-5.6-luna
      effort: medium
      write_mode: allowed
```

SpaceDock은 provider session을 만들 때 다음 흐름으로 Codex를 직접 실행합니다.

```text
codex app-server
    ↓
initialize / initialized
    ↓
첫 turn: thread/start
후속 turn: thread/resume
    ↓
turn/start
    ↓
turn/completed
```

`agent_continue`는 같은 Codex thread ID를 `thread/resume`으로 이어가므로 provider conversation context가 유지됩니다. Profile `instructions`는 첫 `agent_run`에서만 task 앞에 붙고 `agent_continue`에서는 다시 붙지 않습니다.

`write_mode`는 Codex sandbox로 변환됩니다.

```text
read_only   -> read-only / readOnly
allowed     -> workspace-write / workspaceWrite(networkAccess=true)
full_access -> danger-full-access / dangerFullAccess
```

SpaceDock은 Codex에 `approvalPolicy=never`를 사용하므로 비대화식 subagent turn 중 별도 approval prompt를 기다리지 않습니다. 필요한 권한 범위는 Workspace permission과 `write_mode`를 통해 사전에 결정해야 합니다.

`agents.codex.command` 기본값은 `codex`이며 PATH에서 해석합니다. 다만 systemd user service의 PATH는 로그인 shell과 다를 수 있습니다. nvm 등으로 Codex CLI를 설치했다면 다음처럼 절대 경로 지정이 더 안전합니다.

```yaml
agents:
  codex:
    command: /home/ubuntu/.nvm/versions/node/v22.23.2/bin/codex
```

## Copilot ACP provider

GitHub Copilot은 ACP provider로 연결합니다. ACP endpoint `command`는 **절대 실행 파일 경로**여야 합니다.

```yaml
agents:
  profiles:
    - id: copilot1
      name: 코파일럿1호
      description: Copilot ACP worker
      provider: acp
      endpoint_id: copilot
      instructions: 전달받은 패치 명세만 구현한다.
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

Copilot CLI가 PATH에 설치되어 있어도 ACP endpoint는 명시적 절대 경로를 사용합니다. 예를 들어 `which copilot`로 실제 경로를 확인한 뒤 설정할 수 있습니다.

`provider: acp` profile에서는 `endpoint_id`, `permission_policy`, `mode_id`, `config_options`를 사용합니다. 반대로 `model`, `effort`, `write_mode`는 Codex provider 전용입니다.

## systemd 상시 실행

Linux 운영 기본 방식은 user systemd service입니다.

```sh
spacedock service install
spacedock service status
```

추가 명령은 다음과 같습니다.

```sh
spacedock service start
spacedock service stop
spacedock service restart
spacedock service status
spacedock service uninstall
```

`service install`은 현재 SpaceDock 실행 파일과 절대 config 경로를 사용해 `~/.config/systemd/user/spacedock.service`를 생성하고 `systemctl --user enable --now spacedock.service`를 수행합니다.

SpaceDock 자체 HTTP listener는 보안을 위해 loopback(`127.0.0.1`/`::1`/`localhost`)만 허용합니다. 외부 HTTPS 주소는 사용자가 관리하는 reverse proxy 또는 tunnel이 SpaceDock의 `127.0.0.1:8766`으로 전달해야 합니다.

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

SpaceDock은 Cloudflare Tunnel/ngrok 설정, tunnel credential, `sudo`, `loginctl enable-linger`를 자동 변경하지 않습니다. 로그인 이후에도 user service가 계속 실행되어야 한다면 `loginctl enable-linger`는 운영자가 직접 설정합니다.

## ChatGPT 연결과 OAuth

ChatGPT에는 다음 MCP URL을 등록합니다.

```text
https://spacedock.example.com/mcp
```

HTTP MCP는 static bearer token이 아니라 OAuth access token으로 보호됩니다. SpaceDock은 다음 흐름을 제공합니다.

- Protected Resource Metadata
- Authorization Server Metadata
- Dynamic Client Registration 호환
- Authorization Code + PKCE S256
- owner token 승인 페이지
- access/refresh token
- refresh token rotation
- scope/resource 검증
- RFC 9207 authorization response `iss`

승인 페이지가 열리면 `~/.spacedock/oauth-owner.token`의 내용을 입력합니다. 이 값은 OAuth client access token이 아니라 **서버 소유자가 연결을 승인하기 위한 비밀값**입니다.

`trust_proxy: true`는 신뢰하는 reverse proxy가 `X-Forwarded-For`를 올바르게 설정하는 경우에만 사용하십시오. 이 값은 OAuth rate limiting의 client IP 판정에 영향을 줍니다.

## Allowed Root와 Workspace

Allowed Root는 프로젝트 하나가 아니라 ChatGPT가 접근할 수 있는 **상위 권한 경계**입니다. 예를 들어 다음 설정은 `/home/ubuntu/github` 아래의 프로젝트만 열 수 있게 합니다.

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

ChatGPT 측 흐름은 다음과 같습니다.

```text
workspace_list
    ↓
workspace_open(root_id="github", path="spacedock", mode="checkout")
    ↓
ws_... workspace ID
    ↓
workspace-scoped tools
```

`path`는 Allowed Root 기준 상대 경로입니다. 절대 경로, `..` 탈출, URI/UNC/volume path, symlink를 통한 Root 이탈은 거부됩니다.

### checkout

기존 checkout을 그대로 사용합니다.

```text
workspace_open(root_id="github", path="spacedock", mode="checkout")
```

### worktree

격리된 agent 작업에는 managed Git worktree를 사용할 수 있습니다.

```text
workspace_open(
  root_id="github",
  path="spacedock",
  mode="worktree",
  base_ref="HEAD"
)
```

worktree는 `<state_dir>/worktrees/<workspace-id>` 아래 detached 상태로 생성됩니다. 선택한 project path의 상위 Git repository로 경계를 자동 확대하지 않습니다.

수정사항이 있는 managed worktree는 일반 `workspace_close`로 제거되지 않으며 `WORKTREE_DIRTY` 오류를 반환합니다. 의도적으로 폐기할 때만 `workspace_discard(force=true)`를 사용합니다.

Workspace와 managed worktree 정보는 `workspaces.json`에 저장되어 SpaceDock/systemd 재시작 후 복구됩니다. Codex app-server/ACP process와 agent execution session은 process-local이므로 재시작 후 복구되지 않습니다.

## Generic ACP 도구

Copilot subagent 외에도 ACP endpoint를 저수준에서 직접 사용할 수 있습니다.

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

`acp_connect`는 단순 initialize에서 끝나지 않고 실제 `session/new`까지 수행합니다. `acp_prompt`는 같은 remote session에 `session/prompt`를 실행하고, `session/update` 이벤트를 bounded event ring으로 수집합니다. `agent_thought_chunk`는 저장/노출하지 않습니다.

`permission_policy`는 `manual` 또는 `allow_once`입니다. `manual`에서는 ACP permission request를 `acp_interactions`로 노출하고 `acp_respond`로 선택할 수 있습니다. 영구 허용(`always`) 류 선택지는 자동 노출/선택하지 않습니다.

## Agent 도구

고수준 Agent 도구는 provider-agnostic입니다.

```text
agent_list
agent_run
agent_show
agent_continue
agent_stop
```

- `agent_run`: profile의 `provider`에 따라 Codex CLI 또는 ACP session을 생성하고 bounded worker turn을 시작합니다.
- `agent_show`: provider 종류와 무관하게 generic run 상태와 final response를 반환합니다.
- `agent_continue`: 같은 provider session을 재사용합니다. Codex는 같은 thread, ACP는 같은 remote session입니다.
- `agent_stop`: running turn을 취소하고 provider session을 종료합니다.
- `agents.max_concurrent`: agent process 수가 아니라 동시에 running인 agent turn 수를 제한합니다.

Agent record에는 `provider`와 `provider_session_id`가 포함됩니다. Codex의 `provider_session_id`는 Codex thread ID이고, ACP provider에서는 SpaceDock의 local ACP session ID입니다.

## 제공 도구

SpaceDock은 shell 하나에 모든 기능을 몰아넣지 않고 구조화된 MCP tool을 제공합니다.

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

File/Git path와 command working directory는 열린 Workspace를 기준으로 제한됩니다.

## 중요한 보안 경계

`fs.*`와 Workspace path resolver는 Allowed Root를 벗어나지 못하도록 검사합니다. 하지만 `command.execute`는 OS sandbox가 아닙니다. 실행한 subprocess는 SpaceDock을 실행한 **로컬 OS 사용자 권한**을 가지므로 shell 명령 자체가 접근 가능한 시스템 자원까지 별도로 격리하지는 않습니다.

민감 경로 deny layer는 구조화된 filesystem tool에만 적용됩니다. `.ssh`, `.aws`, `.gnupg`, `.env`와 `.env.*`, credentials 및 SSH key/`known_hosts` 이름, `.pem`, `.key`, `.pfx`, `.p12` 확장자를 항상 차단합니다. `security.sensitive_paths.additional_patterns`에 component glob을 추가할 수 있지만 기본 목록은 해제할 수 없습니다. 직접 접근은 구조화된 `PERMISSION_DENIED`로 반환하고 broad listing/search에서는 민감 항목을 숨깁니다. 이 계층은 `command.execute`나 임의 shell command를 제한하지 않습니다.

Codex의 `write_mode` sandbox는 Codex provider 자체의 실행 정책이며 SpaceDock의 Allowed Root/permission 모델을 대체하지 않습니다. ACP provider의 permission request 역시 SpaceDock의 `agent.execute`/`acp.connect` 권한과 별개 층입니다.

따라서 다음 원칙을 권장합니다.

- 신뢰하는 개발 디렉터리만 Allowed Root로 등록
- 필요 없는 Root에는 `command.execute`, `fs.write`, `agent.execute`를 부여하지 않기
- Codex worker에는 목적에 필요한 최소 `write_mode` 사용
- SpaceDock HTTP listener는 loopback 유지
- 외부 노출은 HTTPS reverse proxy/tunnel 사용
- owner token과 OAuth state directory를 외부에 공유하지 않기
- `trust_proxy`는 신뢰하는 proxy 앞에서만 활성화

## stdio 모드

원격 ChatGPT 운영의 HTTP + OAuth + systemd 경로는 그대로 유지됩니다. `stdio`는 로컬 MCP transport이며 SpaceDock은 `command: spacedock`, `args: serve --stdio`, `transport: stdio`를 보장합니다(사용자 설정에는 `--config <path>` 추가). 특정 client JSON schema를 전제하지 않으며 stdio에서는 HTTP OAuth middleware를 사용하지 않습니다.

```sh
spacedock serve --stdio
```

## 소스에서 검증/빌드

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/spacedock
npm run build:npm-binaries
```

Go module 경로는 `github.com/starlove7/spacedock`입니다.
