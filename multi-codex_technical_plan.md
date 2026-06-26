# multi-codex 项目技术规划与实施文档

版本：v0.1  
日期：2026-06-25  
状态：技术规划草案，可作为 `docs/architecture.md` 与 `docs/implementation.md` 的初版合并文档。

---

## 0. 文档目标

`multi-codex` 是一个企业级多 Codex 协作、调度、隔离、审计与交付平台。它面向公司内部工程项目，解决多个 Codex CLI 实例在同一代码库或多代码库中并行工作的治理问题。

核心目标不是“让多个 Codex 随意互聊”，而是建立一个可审计、可约束、可复盘、可部署的工程系统：

```text
Human Lead / TL
    ↓
Main Codex / Gateway Codex
    ↓ MCP Gateway
multi-codex backend
    ↓
Docker Executor / SSH Executor
    ↓
Feature Codex / Test Codex / Audit Codex / Git Sync Codex / Docs Codex
```

本项目技术栈：

- 前端：Vite 8 + TypeScript，默认 UI 框架采用 React。
- 后端：Go 1.25。
- 数据库：PostgreSQL 18。
- 部署：Docker / Docker Compose 为第一阶段部署方案，后续可扩展 Kubernetes。
- Worker 管理：支持 Docker 容器执行，也支持 SSH 管理独立虚拟机执行。
- Agent 协议：MCP Gateway 作为 Main Codex 与平台能力之间的受控边界。
- Agent 工作法：Skills + AGENTS.md + Task Envelope。

---

## 1. 项目定位

### 1.1 项目名称

```text
multi-codex
```

可选命令行简称：

```text
mcx
```

### 1.2 一句话定义

`multi-codex` 是一个通过 MCP Gateway 调度多个隔离 Codex Worker 的企业级工程协作平台，用于实现需求拆解、任务分配、作用域限制、代码实现、测试验证、审计审查、Git 同步和 PR 交付。

### 1.3 核心设计原则

1. Main Codex 不直接写业务代码。
2. 子 Codex 不直接互相通信。
3. 所有能力调用必须经过 MCP Gateway。
4. 每个任务必须先形成结构化 Task Envelope。
5. 每个 Worker 必须有独立执行环境、独立 worktree、独立 role、独立 scope。
6. Skill 负责工作方法，不能作为安全边界。
7. Gateway、容器/VM、Git diff、CI、审批共同构成硬边界。
8. Audit Codex 默认 read-only。
9. Git Sync Codex 是唯一可以执行 rebase / PR 准备的专用角色。
10. 最终 merge 仍由人类批准。

---

## 2. 范围定义

### 2.1 MVP 范围

MVP 需要完成从“人类提出需求”到“Codex 生成 PR 草稿”的完整闭环：

1. 创建项目、仓库、Skill、Agent Profile。
2. Main Codex 通过 MCP Gateway 创建任务。
3. Gateway 校验 Task Envelope。
4. Docker Executor 启动 Feature Codex。
5. Worker 在独立 worktree 中执行 `codex exec`。
6. Worker 产出结果、diff、测试报告。
7. Gateway 执行 scope check。
8. Test Codex 独立验证。
9. Audit Codex read-only 审计。
10. Git Sync Codex rebase 并准备 PR。
11. Web Console 展示任务、日志、diff、审计结论和审批状态。

### 2.2 第二阶段范围

1. SSH Executor 管理独立虚拟机。
2. 多 repo、多 project、多团队支持。
3. Worker Pool、并发限制、队列优先级。
4. GitHub/GitLab/Forgejo 集成。
5. OIDC/SSO、RBAC、审批流。
6. 审计日志不可变存储。
7. 成本、token、运行时长统计。
8. Kubernetes 部署。

### 2.3 非目标

MVP 不做：

1. 不做通用 AI 聊天系统。
2. 不允许 Codex Worker 获得任意 shell 能力。
3. 不做跨公司多租户 SaaS。
4. 不自动 merge 到 main。
5. 不把 Skill 当作安全沙箱。
6. 不在 Worker 间建立点对点通信。

---

## 3. 技术栈规划

### 3.1 前端

```text
Vite 8
TypeScript
React
React Router
TanStack Query
Zod
SSE / WebSocket 日志流
CSS Modules / Tailwind 可二选一
```

前端职责：

1. 项目、仓库、任务、Worker、Skill、Profile 管理。
2. 任务运行状态可视化。
3. Codex Worker 日志流展示。
4. Diff、scope check、测试结果、审计结果展示。
5. 审批中心。
6. 节点与执行器管理。
7. 审计日志查询。

### 3.2 后端

```text
Go 1.25
net/http 或 chi router
pgx/v5
sqlc 可选
goose / tern / atlas migrations 可选
zap / slog logging
OpenTelemetry 可选
```

后端服务拆分建议：

```text
cmd/api             Web API 服务
cmd/mcp-gateway     MCP Gateway 服务
cmd/worker-agentd   SSH VM 上的可选 Agent Daemon
cmd/mcxctl          管理 CLI，可选
```

MVP 可先合并为一个二进制：

```text
multi-codex server
```

后续再拆成：

```text
multi-codex-api
multi-codex-gateway
multi-codex-worker-agentd
```

### 3.3 数据库

```text
PostgreSQL 18
```

使用方向：

1. 任务、运行、节点、审批、审计日志存储。
2. JSONB 存储 task envelope、tool call payload、worker result。
3. uuidv7 作为主键默认生成策略。
4. advisory lock 防止同一任务重复调度。
5. LISTEN/NOTIFY 可用于轻量事件通知，生产可替换为 NATS/Redis Streams。

### 3.4 部署

第一阶段部署使用 Docker Compose：

```text
postgres
api
mcp-gateway
web
worker-manager
reverse-proxy
```

注意：这里 Docker 有两层含义：

1. 项目自身的部署方案：用 Docker / Docker Compose 部署 multi-codex 平台。
2. Worker 的一种执行方案：用 Docker Executor 启动隔离 Codex Worker 容器。

### 3.5 Worker 执行环境

支持两种执行器：

```text
Docker Executor：
- 同机或同集群启动 Worker 容器。
- 每个任务一个容器或一组容器。
- 默认网络关闭或受控网络。
- 通过挂载独立 worktree 限定文件系统边界。

SSH Executor：
- 管理独立虚拟机或裸机。
- 每台 VM 运行 codex-worker 用户。
- Gateway 通过 SSH 执行受控命令。
- 可选安装 multi-codex-agentd 做更强的远程执行协议。
```

---

## 4. 总体架构

### 4.1 逻辑架构

```text
┌──────────────────────────────────────────────────────┐
│                    Human Lead / TL                    │
│  需求输入、风险审批、最终 PR Review / Merge           │
└──────────────────────────────┬───────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────┐
│                 Web Console / Vite 8                  │
│  任务看板、日志、diff、审批、节点、Skill 管理          │
└──────────────────────────────┬───────────────────────┘
                               │ REST/SSE
                               ▼
┌──────────────────────────────────────────────────────┐
│                    Go API Server                      │
│  Auth / RBAC / Task / Run / Repo / Skill / Approval   │
└──────────────────────────────┬───────────────────────┘
                               │
                ┌──────────────┴──────────────┐
                ▼                             ▼
┌──────────────────────────────┐  ┌──────────────────────────────┐
│        MCP Gateway            │  │        Scheduler             │
│  受控 MCP Tools               │  │  队列、状态机、Worker 选择    │
│  Policy Enforcement           │  │  retry、timeout、并发控制     │
└──────────────┬───────────────┘  └──────────────┬───────────────┘
               │                                  │
               └──────────────┬───────────────────┘
                              ▼
┌──────────────────────────────────────────────────────┐
│                  Executor Layer                       │
│  Docker Executor / SSH Executor                       │
└──────────────┬───────────────────────┬───────────────┘
               │                       │
               ▼                       ▼
┌──────────────────────────────┐  ┌──────────────────────────────┐
│ Docker Codex Worker           │  │ SSH VM Codex Worker           │
│ feature/test/audit/git-sync   │  │ feature/test/audit/git-sync   │
└──────────────┬───────────────┘  └──────────────┬───────────────┘
               │                                  │
               ▼                                  ▼
┌──────────────────────────────────────────────────────┐
│               Git Worktree / Repo Cache               │
└──────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────┐
│                 PostgreSQL 18                         │
│  tasks / runs / events / artifacts / approvals / logs │
└──────────────────────────────────────────────────────┘
```

### 4.2 MCP 架构

```text
Main Codex CLI
    │
    │ MCP Streamable HTTP / stdio
    ▼
MCP Gateway
    ├── task_create
    ├── policy_validate_task
    ├── worker_spawn
    ├── worker_status
    ├── worker_result
    ├── repo_scope_check
    ├── audit_run
    ├── git_prepare_pr
    └── approval_request
```

Main Codex 只连接 MCP Gateway，不直接拥有 Docker、SSH、Git、shell、数据库等能力。

### 4.3 Worker 架构

```text
Worker Runtime
  ├── /workspace        独立 git worktree
  ├── /runs             task.json / result.json / logs
  ├── /skills           只读 Skill 包
  ├── /repo-agents      AGENTS.md / AGENTS.override.md
  └── codex exec        真正执行 Codex CLI
```

---

## 5. 角色模型

### 5.1 系统角色

| 角色 | 说明 | 是否写代码 | 是否可执行 Git 同步 | 默认权限 |
|---|---|---:|---:|---|
| Main Codex | 需求拆解、任务分配、网关决策 | 否 | 否 | MCP Gateway tools |
| Feature Codex | 实现一个受限功能 | 是 | 否 | scoped workspace-write |
| Test Codex | 写测试、跑回归、复现失败 | 主要写测试 | 否 | test paths write |
| Audit Codex | 安全、质量、架构、scope 审计 | 否 | 否 | read-only |
| Git Sync Codex | rebase、冲突归并、PR 准备 | 有限 | 是，需审批 | integration workspace-write |
| Docs Codex | 文档、changelog、使用说明 | 是 | 否 | docs write |
| Release Codex | release notes、升级说明 | 有限 | 否 | release paths write |

### 5.2 平台用户角色

| 角色 | 权限 |
|---|---|
| Owner | 全部权限，包括系统配置、节点、密钥、审批策略 |
| Admin | 管理项目、仓库、用户、节点、Worker Profile |
| Tech Lead | 创建任务、审批高风险操作、发起 PR |
| Reviewer | 查看 diff、审计结果、审批或驳回 |
| Operator | 管理 Worker 节点、重试、取消任务 |
| Auditor | 只读查看审计日志 |
| Viewer | 只读查看任务和结果 |

---

## 6. 核心概念模型

### 6.1 Project

一个业务项目或工程域。例如：

```text
payment-platform
customer-portal
internal-tools
```

### 6.2 Repository

一个 Git 仓库配置：

```json
{
  "id": "...",
  "project_id": "...",
  "name": "payment-api",
  "provider": "github",
  "remote_url": "git@github.com:company/payment-api.git",
  "default_branch": "main"
}
```

### 6.3 Skill

Skill 定义某个 Codex 角色的工作方法，不定义硬权限。

示例：

```text
company-main-gateway
company-feature-worker
company-test-worker
company-audit-worker
company-git-sync
```

### 6.4 Agent Profile

Agent Profile 定义运行参数：

```json
{
  "name": "feature-worker-go-node",
  "role": "feature",
  "model": "gpt-5.5",
  "sandbox_mode": "workspace-write",
  "approval_policy": "never",
  "network": false,
  "executor": "docker",
  "image": "multi-codex/codex-worker:go1.25-node-vite8"
}
```

### 6.5 Task Envelope

Task Envelope 是单次任务的结构化合同。所有 Worker 必须根据它运行。

### 6.6 Run

一次 Codex Worker 执行记录。一个 Task 可以有多个 Run：

```text
feature run
scope-check run
test run
audit run
git-sync run
```

### 6.7 Artifact

Worker 产物：

```text
result.json
worker.log
diff.patch
test-output.log
audit-findings.json
pr-body.md
```

---

## 7. Task Envelope 规范

### 7.1 示例

```json
{
  "task_id": "FEAT-123",
  "project_id": "payment-platform",
  "repository_id": "payment-api",
  "title": "Add refund status audit trail",
  "base_branch": "origin/main",
  "target_branch": "codex/feat-123-refund-audit-trail",
  "role": "feature",
  "skill": "company-feature-worker",
  "agent_profile": "feature-worker-go-node",
  "executor": "docker",
  "allowed_paths": [
    "services/payments/**",
    "tests/payments/**"
  ],
  "forbidden_paths": [
    ".github/**",
    "infra/**",
    "secrets/**",
    "services/auth/**",
    "package-lock.json",
    "pnpm-lock.yaml"
  ],
  "allowed_commands": [
    "pnpm test tests/payments/refund-status.test.ts",
    "pnpm lint services/payments",
    "pnpm typecheck"
  ],
  "network": false,
  "objective": "Add an audit event whenever refund status changes.",
  "acceptance_criteria": [
    "Exactly one audit event is written per real status change.",
    "Duplicate status updates do not create duplicate audit events.",
    "Existing refund behavior remains compatible."
  ],
  "stop_conditions": [
    "Need to change auth, infra, CI, or dependency files.",
    "Need a product decision not present in the task.",
    "Required tests cannot run in the assigned environment."
  ],
  "required_outputs": [
    "changed_files",
    "summary",
    "tests_run",
    "risks",
    "needs_human"
  ],
  "policy": {
    "allow_push": false,
    "allow_dependency_change": false,
    "allow_infra_change": false,
    "require_audit": true,
    "require_tests": true,
    "require_human_before_pr": true
  }
}
```

### 7.2 校验规则

Gateway 在创建任务时必须校验：

1. `allowed_paths` 不能为空。
2. `forbidden_paths` 必须包含 CI、infra、secret、lockfile 等敏感路径。
3. `role` 必须匹配 Agent Profile。
4. `skill` 必须存在且处于启用状态。
5. `executor` 必须有可用节点。
6. `base_branch` 必须存在。
7. `target_branch` 不得覆盖保护分支。
8. 高风险操作必须带审批策略。

---

## 8. MCP Gateway 设计

### 8.1 定位

MCP Gateway 是 Main Codex 唯一可见的企业能力入口。

它不是一个简单转发器，而是 Policy Enforcement Point：

```text
Main Codex 调用工具
    ↓
MCP Gateway 校验身份、角色、任务、scope、审批状态
    ↓
Gateway 调用内部服务或 Executor
    ↓
Gateway 记录 tool call、事件和审计日志
    ↓
返回结构化结果
```

### 8.2 工具分组

#### Task tools

```text
task_create
task_get
task_update_status
task_list
task_add_dependency
```

#### Policy tools

```text
policy_validate_task
policy_validate_diff
policy_validate_command
policy_validate_dependency_change
policy_validate_secret_exposure
```

#### Worker tools

```text
worker_spawn
worker_status
worker_logs
worker_result
worker_cancel
worker_retry
```

#### Repo tools

```text
repo_create_worktree
repo_diff_files
repo_diff_stat
repo_read_diff
repo_scope_check
repo_cleanup_worktree
```

#### Test tools

```text
test_run_required
test_collect_report
```

#### Audit tools

```text
audit_run
audit_collect_findings
```

#### Git tools

```text
git_fetch
git_rebase
git_prepare_pr
git_push_for_review
git_create_pr
```

#### Approval tools

```text
approval_request
approval_status
approval_require_human
```

### 8.3 工具原则

必须避免暴露：

```text
shell_exec_raw
docker_run_raw
ssh_exec_raw
git_push_raw
git_merge_raw
kubectl_exec_raw
secret_read_raw
write_file_anywhere
```

如确实需要执行命令，必须模板化：

```text
run_allowed_command(task_id, command_id)
run_required_tests(task_id)
run_lint(task_id)
run_typecheck(task_id)
```

### 8.4 MCP Tool 示例

```json
{
  "name": "worker_spawn",
  "description": "Start a role-specific Codex worker in an isolated Docker container or SSH VM using a validated task envelope.",
  "input_schema": {
    "type": "object",
    "required": ["task_id", "role", "executor"],
    "properties": {
      "task_id": { "type": "string" },
      "role": {
        "type": "string",
        "enum": ["feature", "test", "audit", "git-sync", "docs"]
      },
      "executor": {
        "type": "string",
        "enum": ["docker", "ssh"]
      }
    }
  }
}
```

---

## 9. Skill 设计

### 9.1 Skill 目录

```text
multi-codex/
  skills/
    company-main-gateway/
      SKILL.md
      references/
        task-envelope.md
        go-no-go-policy.md
    company-feature-worker/
      SKILL.md
      references/
        implementation-rules.md
    company-test-worker/
      SKILL.md
      references/
        testing-policy.md
    company-audit-worker/
      SKILL.md
      references/
        security-review-checklist.md
        architecture-review-checklist.md
    company-git-sync/
      SKILL.md
      scripts/
        generate_pr_body.py
```

### 9.2 Main Gateway Skill

```md
---
name: company-main-gateway
description: Use this skill when acting as Main Codex to decompose engineering work, assign scoped Codex workers through the MCP Gateway, and make go/no-go decisions.
---

# Company Main Gateway Skill

You are Main Codex.

You do not implement production code directly.

Required workflow:
1. Convert the human request into one or more task envelopes.
2. Call policy_validate_task.
3. Spawn only role-specific workers.
4. Wait for structured worker results.
5. Run repo_scope_check after every code-producing worker.
6. Require tests and audit before git sync.
7. Produce a go/no-go decision.

Hard rules:
- Do not call raw shell.
- Do not approve scope violations.
- Do not allow worker-to-worker direct communication.
- Do not merge to main.
```

### 9.3 Feature Worker Skill

```md
---
name: company-feature-worker
description: Use this skill when implementing one scoped product or backend feature in allowed paths only.
---

# Company Feature Worker Skill

Rules:
- Modify only files under allowed_paths.
- Never modify forbidden_paths.
- Do not push.
- Do not rebase.
- Do not change dependencies unless explicitly allowed.
- Stop if implementation requires a product or architecture decision not present in the task.

Required output:
{
  "status": "done | blocked | failed",
  "changed_files": [],
  "summary": "",
  "tests_run": [],
  "tests_failed": [],
  "risks": [],
  "needs_human": []
}
```

### 9.4 Audit Worker Skill

```md
---
name: company-audit-worker
description: Use this skill for read-only security, correctness, architecture, and maintainability review of a Codex-generated change.
---

# Company Audit Worker Skill

Mode: read-only.

Review areas:
- scope creep
- auth bypass
- permission regression
- PII leakage
- secret exposure
- race condition
- data migration risk
- backward compatibility
- missing tests
- unnecessary refactor

Required output:
{
  "status": "pass | fail",
  "blockers": [],
  "high": [],
  "medium": [],
  "low": [],
  "scope_concerns": [],
  "recommended_next_action": ""
}
```

---

## 10. 执行器设计

### 10.1 Executor 接口

```go
type Executor interface {
    Prepare(ctx context.Context, task TaskEnvelope) (*RunContext, error)
    Start(ctx context.Context, rc *RunContext) (*WorkerHandle, error)
    StreamLogs(ctx context.Context, runID uuid.UUID) (<-chan RunEvent, error)
    Stop(ctx context.Context, runID uuid.UUID) error
    Collect(ctx context.Context, runID uuid.UUID) (*RunResult, error)
    Cleanup(ctx context.Context, runID uuid.UUID) error
}
```

### 10.2 Docker Executor

Docker Executor 流程：

```text
1. Gateway 创建 task run。
2. Repo Manager 创建独立 worktree。
3. 渲染 /runs/task.json、/runs/prompt.md、/runs/skill/。
4. 启动 Docker 容器。
5. 容器内执行 codex exec。
6. 日志写入 /runs/worker.log，并通过 Gateway 流式读取。
7. Worker 写入 /runs/result.json。
8. Gateway 收集结果并做 scope check。
9. 容器停止并保留 worktree/artifacts。
```

Docker Worker 挂载建议：

```text
/workspace        RW，仅本任务 worktree
/runs             RW，仅本任务产物目录
/skills           RO，本角色 Skill
/cache            可选，只读或隔离包缓存
```

容器限制建议：

```text
--network none 默认关闭网络
--cpus 2
--memory 4g
--pids-limit 512
--read-only 根文件系统可选
--cap-drop ALL
非 root 用户运行
```

需要联网安装依赖时必须走审批：

```text
policy.network = true
approval.required = true
egress allowlist = package registry / git provider
```

### 10.3 SSH Executor

SSH Executor 面向独立 VM 或裸机 Worker。

#### VM 前置要求

```text
OS: Ubuntu LTS / Debian / Rocky Linux 均可
User: codex-worker 非 root 用户
Tools: git, codex CLI, docker 可选, node/pnpm/go 按 profile 安装
Workspace: /var/lib/multi-codex/workspaces
Runs: /var/lib/multi-codex/runs
Logs: /var/log/multi-codex
```

#### SSH 直连模式

Gateway 使用受限 SSH key：

```text
Gateway
  ├── ssh codex-worker@vm multi-codex-agentd prepare --task-id FEAT-123
  ├── rsync task.json / skills / prompt
  ├── ssh codex-worker@vm multi-codex-agentd start --task-id FEAT-123
  ├── ssh codex-worker@vm multi-codex-agentd status --run-id ...
  └── rsync result.json / logs / patch back
```

#### agentd 模式

更推荐长期使用 agentd：

```text
Gateway HTTPS/mTLS -> multi-codex-agentd on VM
```

SSH 只用于：

```text
bootstrap
upgrade agentd
emergency maintenance
```

#### SSH 安全要求

1. 每个环境独立 SSH key。
2. VM 上使用 `codex-worker` 非 root 用户。
3. 禁止密码登录。
4. 限制来源 IP。
5. 可使用 forced command 限制 SSH key 只能执行 agentd。
6. 每个任务独立 worktree 和 run 目录。
7. Gateway 采集 host key fingerprint，防止中间人攻击。
8. 定期轮换密钥。

---

## 11. Git 工作流

### 11.1 分支命名

```text
codex/{task_id}/{role}/{slug}
```

示例：

```text
codex/feat-123/feature/refund-audit-trail
codex/feat-123/test/refund-audit-trail
codex/feat-123/integration/refund-audit-trail
```

### 11.2 Worktree 策略

每个 Worker 使用独立 worktree：

```text
/var/lib/multi-codex/worktrees/{repo_id}/{task_id}/{role}
```

### 11.3 Gate 顺序

```text
Task validated
  ↓
Feature Codex done
  ↓
Scope check passed
  ↓
Test Codex passed
  ↓
Audit Codex passed
  ↓
Git Sync Codex rebase
  ↓
CI passed
  ↓
Human review
  ↓
Merge
```

### 11.4 禁止项

Worker 默认禁止：

```text
git push
git merge main
git rebase origin/main
修改保护分支
force push
修改 CI/infra/secrets/auth/billing/lockfile
```

Git Sync Codex 例外，但仍需 Gateway 和人类审批。

---

## 12. Scope 与 Policy

### 12.1 Scope Check

Scope Check 核心逻辑：

```text
git diff --name-only base...HEAD
  ↓
每个 changed file 必须匹配 allowed_paths
  ↓
任何 changed file 不得匹配 forbidden_paths
  ↓
否则 BLOCKED_BY_SCOPE
```

### 12.2 高风险路径默认禁止

```text
.github/**
.gitlab/**
infra/**
k8s/**
terraform/**
secrets/**
.env*
**/*secret*
**/*credential*
package-lock.json
pnpm-lock.yaml
go.sum
```

注意：`go.sum`、lockfile 并非永远禁止，但必须由任务显式允许。

### 12.3 高风险操作需要审批

```text
开启网络
新增依赖
修改 lockfile
修改 CI/CD
修改 infra
修改 auth / permission
修改 billing / payment
push branch
force push
访问生产数据库
读取 secrets
```

---

## 13. 数据库设计

### 13.1 表概览

```text
organizations
users
memberships
projects
repositories
skills
skill_versions
agent_profiles
executor_nodes
tasks
task_dependencies
runs
run_events
tool_calls
artifacts
scope_checks
review_findings
approvals
audit_logs
```

### 13.2 核心 DDL 草案

```sql
CREATE TYPE task_status AS ENUM (
  'draft',
  'validated',
  'queued',
  'running',
  'blocked',
  'failed',
  'completed',
  'cancelled'
);

CREATE TYPE run_status AS ENUM (
  'queued',
  'preparing',
  'running',
  'succeeded',
  'failed',
  'blocked',
  'cancelled',
  'timed_out'
);

CREATE TYPE run_role AS ENUM (
  'main',
  'feature',
  'test',
  'audit',
  'git_sync',
  'docs',
  'release'
);

CREATE TYPE executor_kind AS ENUM ('docker', 'ssh');

CREATE TABLE organizations (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  name text NOT NULL,
  slug text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  email text NOT NULL UNIQUE,
  display_name text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, user_id)
);

CREATE TABLE projects (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name text NOT NULL,
  slug text NOT NULL,
  description text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, slug)
);

CREATE TABLE repositories (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name text NOT NULL,
  provider text NOT NULL,
  remote_url text NOT NULL,
  default_branch text NOT NULL DEFAULT 'main',
  local_mirror_path text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE skills (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name text NOT NULL,
  description text NOT NULL DEFAULT '',
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, name)
);

CREATE TABLE skill_versions (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  skill_id uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
  version text NOT NULL,
  content_hash text NOT NULL,
  path text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (skill_id, version)
);

CREATE TABLE agent_profiles (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  project_id uuid REFERENCES projects(id) ON DELETE CASCADE,
  name text NOT NULL,
  role run_role NOT NULL,
  model text NOT NULL,
  sandbox_mode text NOT NULL,
  approval_policy text NOT NULL,
  executor executor_kind NOT NULL,
  image text,
  network_enabled boolean NOT NULL DEFAULT false,
  config jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE executor_nodes (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  kind executor_kind NOT NULL,
  name text NOT NULL,
  address text,
  labels jsonb NOT NULL DEFAULT '{}',
  capacity jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'active',
  last_seen_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tasks (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  task_key text NOT NULL,
  title text NOT NULL,
  status task_status NOT NULL DEFAULT 'draft',
  envelope jsonb NOT NULL,
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (project_id, task_key)
);

CREATE TABLE task_dependencies (
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  depends_on_task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, depends_on_task_id)
);

CREATE TABLE runs (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  role run_role NOT NULL,
  status run_status NOT NULL DEFAULT 'queued',
  executor executor_kind NOT NULL,
  executor_node_id uuid REFERENCES executor_nodes(id),
  branch text,
  worktree_path text,
  skill_version_id uuid REFERENCES skill_versions(id),
  agent_profile_id uuid REFERENCES agent_profiles(id),
  started_at timestamptz,
  finished_at timestamptz,
  result jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE run_events (
  id bigserial PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  seq bigint NOT NULL,
  level text NOT NULL DEFAULT 'info',
  event_type text NOT NULL,
  message text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, seq)
);

CREATE TABLE tool_calls (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
  caller text NOT NULL,
  tool_name text NOT NULL,
  input jsonb NOT NULL DEFAULT '{}',
  output jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz
);

CREATE TABLE artifacts (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  kind text NOT NULL,
  name text NOT NULL,
  path text NOT NULL,
  sha256 text,
  size_bytes bigint,
  metadata jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE scope_checks (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
  base_ref text NOT NULL,
  status text NOT NULL,
  changed_files jsonb NOT NULL DEFAULT '[]',
  violations jsonb NOT NULL DEFAULT '[]',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE review_findings (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
  severity text NOT NULL,
  category text NOT NULL,
  file_path text,
  line_start integer,
  line_end integer,
  title text NOT NULL,
  detail text NOT NULL,
  status text NOT NULL DEFAULT 'open',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE approvals (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  requested_by uuid REFERENCES users(id),
  approved_by uuid REFERENCES users(id),
  approval_type text NOT NULL,
  status text NOT NULL DEFAULT 'pending',
  reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  decided_at timestamptz
);

CREATE TABLE audit_logs (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  org_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
  actor_type text NOT NULL,
  actor_id text NOT NULL,
  action text NOT NULL,
  resource_type text NOT NULL,
  resource_id text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_tasks_project_status ON tasks(project_id, status);
CREATE INDEX idx_runs_task_role ON runs(task_id, role);
CREATE INDEX idx_run_events_run_seq ON run_events(run_id, seq);
CREATE INDEX idx_audit_logs_org_created ON audit_logs(org_id, created_at DESC);
```

---

## 14. 后端工程结构

```text
multi-codex/
  cmd/
    api/
      main.go
    mcp-gateway/
      main.go
    worker-agentd/
      main.go
    mcxctl/
      main.go

  internal/
    api/
      router.go
      middleware.go
      handlers/
    auth/
      rbac.go
      session.go
    config/
      config.go
    db/
      queries/
      migrations/
      store.go
    domain/
      task.go
      run.go
      skill.go
      policy.go
    mcp/
      server.go
      tools_task.go
      tools_worker.go
      tools_repo.go
      tools_git.go
      tools_audit.go
    executor/
      executor.go
      docker/
        docker_executor.go
      ssh/
        ssh_executor.go
    codex/
      prompt.go
      runner.go
      result.go
    git/
      repo_manager.go
      worktree.go
      diff.go
    policy/
      validator.go
      scope.go
      approvals.go
    scheduler/
      scheduler.go
      state_machine.go
    artifact/
      store.go
      local.go
      s3.go
    observability/
      logging.go
      metrics.go
      tracing.go

  apps/
    web/
      package.json
      vite.config.ts
      src/

  deployments/
    docker/
      compose.yaml
      Dockerfile.api
      Dockerfile.web
      Dockerfile.worker
      nginx.conf

  skills/
    company-main-gateway/
    company-feature-worker/
    company-test-worker/
    company-audit-worker/
    company-git-sync/

  docs/
    architecture.md
    implementation.md
    security.md
    operations.md

  scripts/
    bootstrap-dev.sh
    scope-check.sh
    seed-demo.sh
```

---

## 15. API 设计

### 15.1 REST API

```text
GET    /healthz
GET    /readyz

POST   /api/v1/auth/login
POST   /api/v1/auth/logout
GET    /api/v1/me

GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/{project_id}
PATCH  /api/v1/projects/{project_id}

GET    /api/v1/projects/{project_id}/repositories
POST   /api/v1/projects/{project_id}/repositories
GET    /api/v1/repositories/{repository_id}
PATCH  /api/v1/repositories/{repository_id}

GET    /api/v1/projects/{project_id}/skills
POST   /api/v1/projects/{project_id}/skills
GET    /api/v1/skills/{skill_id}/versions
POST   /api/v1/skills/{skill_id}/versions

GET    /api/v1/projects/{project_id}/agent-profiles
POST   /api/v1/projects/{project_id}/agent-profiles
PATCH  /api/v1/agent-profiles/{profile_id}

GET    /api/v1/projects/{project_id}/tasks
POST   /api/v1/projects/{project_id}/tasks
GET    /api/v1/tasks/{task_id}
PATCH  /api/v1/tasks/{task_id}
POST   /api/v1/tasks/{task_id}/validate
POST   /api/v1/tasks/{task_id}/start
POST   /api/v1/tasks/{task_id}/cancel

GET    /api/v1/tasks/{task_id}/runs
GET    /api/v1/runs/{run_id}
GET    /api/v1/runs/{run_id}/events
GET    /api/v1/runs/{run_id}/events/stream
GET    /api/v1/runs/{run_id}/artifacts

POST   /api/v1/tasks/{task_id}/scope-check
POST   /api/v1/tasks/{task_id}/audit
POST   /api/v1/tasks/{task_id}/prepare-pr

GET    /api/v1/approvals
POST   /api/v1/approvals/{approval_id}/approve
POST   /api/v1/approvals/{approval_id}/reject

GET    /api/v1/executor-nodes
POST   /api/v1/executor-nodes
PATCH  /api/v1/executor-nodes/{node_id}

GET    /api/v1/audit-logs
```

### 15.2 SSE 日志流

```text
GET /api/v1/runs/{run_id}/events/stream
```

事件示例：

```json
{
  "seq": 182,
  "level": "info",
  "event_type": "worker_log",
  "message": "pnpm test tests/payments/refund-status.test.ts passed",
  "payload": {
    "command": "pnpm test tests/payments/refund-status.test.ts",
    "exit_code": 0
  },
  "created_at": "2026-06-25T10:10:00Z"
}
```

---

## 16. 前端设计

### 16.1 页面结构

```text
/dashboard
/projects
/projects/:projectId
/projects/:projectId/repos
/projects/:projectId/tasks
/tasks/:taskId
/runs/:runId
/skills
/agent-profiles
/executor-nodes
/approvals
/audit-logs
/settings
```

### 16.2 关键页面

#### Task Detail

展示：

```text
任务摘要
Task Envelope
状态机
Worker Runs
日志流
changed files
scope check
测试结果
审计 findings
审批记录
PR 草稿
```

#### Run Detail

展示：

```text
角色
执行器
节点
worktree
容器 / SSH run id
日志流
artifact 列表
result.json
错误堆栈
```

#### Approval Center

展示：

```text
待审批事项
高风险类型
请求原因
关联 diff
审批历史
approve / reject
```

### 16.3 前端目录

```text
apps/web/src/
  app/
    routes.tsx
    queryClient.ts
  components/
    DiffViewer/
    LogStream/
    StatusBadge/
    JsonViewer/
  features/
    tasks/
    runs/
    skills/
    approvals/
    nodes/
  lib/
    api.ts
    sse.ts
    zod.ts
  pages/
```

---

## 17. Docker 部署方案

### 17.1 Compose 服务

```yaml
services:
  postgres:
    image: postgres:18
    environment:
      POSTGRES_DB: multi_codex
      POSTGRES_USER: multi_codex
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U multi_codex -d multi_codex"]
      interval: 10s
      timeout: 5s
      retries: 5

  api:
    image: multi-codex/api:latest
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      MULTICODEX_DATABASE_URL: postgres://multi_codex:${POSTGRES_PASSWORD}@postgres:5432/multi_codex?sslmode=disable
      MULTICODEX_ARTIFACT_ROOT: /var/lib/multi-codex/artifacts
      MULTICODEX_WORKTREE_ROOT: /var/lib/multi-codex/worktrees
      MULTICODEX_REPO_CACHE_ROOT: /var/lib/multi-codex/repos
    volumes:
      - artifacts:/var/lib/multi-codex/artifacts
      - worktrees:/var/lib/multi-codex/worktrees
      - repo_cache:/var/lib/multi-codex/repos
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8080:8080"

  mcp-gateway:
    image: multi-codex/api:latest
    command: ["multi-codex", "mcp-gateway"]
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      MULTICODEX_DATABASE_URL: postgres://multi_codex:${POSTGRES_PASSWORD}@postgres:5432/multi_codex?sslmode=disable
    ports:
      - "8090:8090"

  web:
    image: multi-codex/web:latest
    depends_on:
      - api
    ports:
      - "3000:80"

volumes:
  postgres_data:
  artifacts:
  worktrees:
  repo_cache:
```

### 17.2 Worker 镜像

```dockerfile
FROM debian:stable-slim

RUN apt-get update && apt-get install -y \
    ca-certificates curl git openssh-client bash jq \
    nodejs npm \
    && rm -rf /var/lib/apt/lists/*

# 根据实际安装方式安装 pnpm、Go 1.25、Codex CLI。
# 企业镜像应固定版本并记录 digest。

RUN useradd -m -u 10001 codex
USER codex
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/multi-codex-worker-entrypoint"]
```

生产建议不要在运行时安装工具链，而是制作多种固定镜像：

```text
multi-codex/codex-worker:base
multi-codex/codex-worker:node-vite8
multi-codex/codex-worker:go1.25
multi-codex/codex-worker:go1.25-node-vite8
multi-codex/codex-worker:audit-readonly
```

---

## 18. 配置文件

### 18.1 multi-codex 配置

```yaml
server:
  listen: ":8080"

mcp_gateway:
  listen: ":8090"
  public_url: "https://multi-codex.example.com/mcp"

database:
  url_env: "MULTICODEX_DATABASE_URL"

storage:
  artifact_root: "/var/lib/multi-codex/artifacts"
  worktree_root: "/var/lib/multi-codex/worktrees"
  repo_cache_root: "/var/lib/multi-codex/repos"

executors:
  docker:
    enabled: true
    default_network: "none"
    worker_image_default: "multi-codex/codex-worker:go1.25-node-vite8"
    cpu_limit: "2"
    memory_limit: "4g"
  ssh:
    enabled: true
    default_user: "codex-worker"
    workspace_root: "/var/lib/multi-codex/workspaces"
    runs_root: "/var/lib/multi-codex/runs"

policy:
  require_scope_check: true
  require_audit: true
  require_tests: true
  require_human_before_push: true
  forbidden_paths_default:
    - ".github/**"
    - "infra/**"
    - "secrets/**"
    - ".env*"
    - "**/*secret*"
    - "**/*credential*"
```

### 18.2 Codex Worker config 模板

```toml
model = "gpt-5.5"
sandbox_mode = "workspace-write"
approval_policy = "never"

[sandbox_workspace_write]
network_access = false
```

---

## 19. 状态机

### 19.1 Task 状态

```text
DRAFT
  ↓
VALIDATED
  ↓
QUEUED
  ↓
RUNNING
  ↓
COMPLETED
```

异常状态：

```text
BLOCKED
FAILED
CANCELLED
```

### 19.2 多阶段状态

```text
TASK_VALIDATED
  ↓
FEATURE_RUNNING
  ↓
FEATURE_DONE
  ↓
SCOPE_CHECKED
  ↓
TEST_RUNNING
  ↓
TEST_PASSED
  ↓
AUDIT_RUNNING
  ↓
AUDIT_PASSED
  ↓
GIT_SYNC_RUNNING
  ↓
PR_READY
  ↓
HUMAN_APPROVED
  ↓
MERGED
```

失败分支：

```text
BLOCKED_BY_SCOPE
BLOCKED_BY_TEST
BLOCKED_BY_AUDIT
BLOCKED_BY_POLICY
BLOCKED_BY_CONFLICT
NEEDS_HUMAN_DECISION
WORKER_FAILED
```

---

## 20. 安全设计

### 20.1 权限边界

```text
Skill = 工作方法
Task Envelope = 本次任务合同
MCP Gateway = 工具权限和策略执行
Docker/VM = 执行环境隔离
Git diff = 文件变更事实
CI = 合并前最终验证
Human Approval = 风险决策
```

### 20.2 密钥管理

1. 数据库中只存 secret reference，不存明文 secret。
2. Worker 默认不注入 Git push token。
3. Worker 默认不注入生产数据库凭证。
4. 日志采集必须做 secret redaction。
5. SSH key、Git token、OpenAI key 必须支持轮换。
6. 高权限 token 只给 Git Sync 或 CI，且需审批。

### 20.3 Docker 安全

1. rootless 或非 root 用户运行 Worker。
2. 默认关闭网络。
3. 限制 CPU、内存、PIDs。
4. 只挂载任务 worktree，不挂载整个宿主机。
5. 避免把 Docker socket 暴露给 Worker 容器。
6. Worker 镜像固定 digest。
7. 容器生命周期与任务绑定。

### 20.4 SSH 安全

1. VM 独立用户 `codex-worker`。
2. 禁止 root login。
3. 禁止 password auth。
4. 使用 known_hosts 校验。
5. forced command 或 agentd 限制可执行命令。
6. 每个任务独立目录。
7. 定时清理 worktree 和 artifacts。

### 20.5 审计日志

必须记录：

```text
谁创建了任务
Main Codex 调用了哪些 MCP tools
Task Envelope 内容和版本
Skill 版本和 hash
Agent Profile
Worker 节点
容器/VM 标识
changed files
scope check 结果
测试命令和结果
audit findings
审批人和审批时间
PR 链接
```

---

## 21. 可观测性

### 21.1 日志

统一结构化日志字段：

```text
trace_id
org_id
project_id
task_id
run_id
role
executor
node_id
tool_name
status
latency_ms
```

### 21.2 指标

```text
multi_codex_tasks_total
multi_codex_runs_total
multi_codex_run_duration_seconds
multi_codex_scope_violations_total
multi_codex_audit_blockers_total
multi_codex_worker_failures_total
multi_codex_executor_capacity
multi_codex_approvals_pending
multi_codex_pr_ready_total
```

### 21.3 Trace

建议使用 OpenTelemetry 追踪：

```text
API request
  → task_create
  → worker_spawn
  → docker_start / ssh_start
  → codex_exec
  → collect_result
  → scope_check
  → audit_run
```

---

## 22. 测试策略

### 22.1 后端测试

```text
unit tests: policy, scope, scheduler, git parser
integration tests: PostgreSQL, Docker executor, SSH executor mock
contract tests: MCP tool schema and response
migration tests: migrate up/down
security tests: forbidden path, command allowlist, secret redaction
```

### 22.2 前端测试

```text
component tests
API mock tests
log streaming tests
diff viewer tests
approval flow tests
```

### 22.3 端到端测试

核心 e2e 场景：

```text
1. 创建项目和仓库
2. 注册 Skill 和 Agent Profile
3. 创建 task envelope
4. Docker Executor 启动 feature worker
5. 产生 diff
6. scope check passed
7. test worker passed
8. audit worker passed
9. git sync prepare PR
```

故障 e2e 场景：

```text
scope violation
worker timeout
Docker image missing
SSH host offline
test failed
audit blocker
rebase conflict
approval rejected
```

---

## 23. 实施路线图

### Phase 0：项目初始化

交付物：

```text
仓库结构
Go module
Vite app
Docker Compose
PostgreSQL migration baseline
CI baseline
```

验收：

```text
docker compose up 可启动 api/web/postgres
/api/healthz 正常
前端可访问
migration 可执行
```

### Phase 1：核心后端与数据模型

交付物：

```text
projects/repositories/tasks/runs API
PostgreSQL DDL
run events
artifacts metadata
basic auth/RBAC skeleton
```

验收：

```text
可创建项目、仓库、任务
可查看 run 列表和事件
数据库迁移稳定
```

### Phase 2：MCP Gateway MVP

交付物：

```text
MCP server
task_create
task_get
worker_spawn
worker_status
worker_result
repo_scope_check
```

验收：

```text
Main Codex 可通过 MCP 创建任务并启动 worker
所有 tool call 写入 tool_calls 和 audit_logs
```

### Phase 3：Docker Executor

交付物：

```text
repo mirror/worktree manager
Docker worker container
codex exec runner
log streaming
result collection
scope check
```

验收：

```text
可启动 Feature Codex 容器
可收集 result.json/logs/diff
scope violation 能被阻断
```

### Phase 4：Skills 与 Agent Profiles

交付物：

```text
Skill 注册与版本管理
Agent Profile 管理
Main/Feature/Test/Audit/GitSync skills
```

验收：

```text
任务可绑定 Skill 版本
run 可记录 skill hash
Worker prompt 可正确渲染
```

### Phase 5：Test/Audit/Git Sync 流程

交付物：

```text
Test Worker
Audit Worker read-only
Git Sync Worker
PR body generation
审批 gate
```

验收：

```text
完整 task → feature → scope → test → audit → git-sync 流程可跑通
阻断项能够阻止 PR 准备
```

### Phase 6：Web Console

交付物：

```text
Dashboard
Task Detail
Run Detail
Log Stream
Diff Viewer
Approval Center
Node Management
```

验收：

```text
人类负责人可以通过 UI 完成创建、查看、审批和复盘
```

### Phase 7：SSH Executor

交付物：

```text
SSH node registration
SSH run lifecycle
agentd optional
host key verification
remote log/result collection
```

验收：

```text
可在独立 VM 上启动 Codex Worker
VM offline/timeout 能正确反馈
```

### Phase 8：安全加固与企业化

交付物：

```text
SSO/OIDC
更完整 RBAC
secret redaction
artifact retention
metrics/tracing
backup/restore
操作手册
```

验收：

```text
具备内部试点上线条件
```

---

## 24. 本地开发启动步骤

### 24.1 初始化

```bash
git clone git@github.com:company/multi-codex.git
cd multi-codex
cp .env.example .env
```

### 24.2 启动依赖

```bash
docker compose -f deployments/docker/compose.yaml up -d postgres
```

### 24.3 后端

```bash
cd services/api # 或项目根目录
export MULTICODEX_DATABASE_URL="postgres://multi_codex:${POSTGRES_PASSWORD}@localhost:5432/multi_codex?sslmode=disable"
go run ./cmd/api
```

### 24.4 MCP Gateway

```bash
go run ./cmd/mcp-gateway
```

### 24.5 前端

```bash
cd apps/web
pnpm install
pnpm dev
```

### 24.6 全量 Docker 启动

```bash
docker compose -f deployments/docker/compose.yaml up -d
```

---

## 25. 示例首个任务闭环

### 25.1 创建任务

```json
{
  "title": "Add scope check API",
  "role": "feature",
  "allowed_paths": [
    "internal/policy/**",
    "internal/api/handlers/scope_check.go",
    "internal/api/router.go",
    "internal/policy/**_test.go"
  ],
  "forbidden_paths": [
    "deployments/**",
    ".github/**",
    "go.mod",
    "go.sum"
  ],
  "acceptance_criteria": [
    "API accepts task_id and base_ref.",
    "API returns changed files and violations.",
    "Unit tests cover allowed and forbidden paths."
  ]
}
```

### 25.2 Main Codex 调用 MCP

```text
task_create
policy_validate_task
worker_spawn(role=feature, executor=docker)
worker_result
repo_scope_check
test_run_required
audit_run
git_prepare_pr
```

### 25.3 Gateway 决策

```json
{
  "decision": "go",
  "scope_check": "passed",
  "tests": "passed",
  "audit": "passed",
  "next_action": "prepare_pr"
}
```

---

## 26. 风险与应对

| 风险 | 影响 | 应对 |
|---|---|---|
| Worker 越权修改 | 破坏仓库安全 | scope check + worktree + forbidden paths + CI gate |
| Worker 获取过多权限 | 泄露密钥或误操作 | 默认无网络、无 push token、无生产 secret |
| 多 Worker Git 冲突 | 任务失败或代码覆盖 | 独立 worktree，Git Sync 专职归并 |
| MCP tool 过宽 | Main Codex 可绕过政策 | 禁止 raw shell，所有 tool 做 policy validation |
| SSH VM 管控弱 | 难审计、难清理 | agentd + forced command + per-task directory |
| 日志泄露 secret | 安全事故 | redaction + secret reference + 日志审计 |
| Skill 版本漂移 | 结果不可复盘 | 记录 skill version/hash |
| 模型输出不稳定 | 任务质量波动 | 结构化输出、审计、测试、人工审批 |

---

## 27. MVP 验收标准

MVP 完成时必须满足：

1. 可以通过 Web Console 创建项目、仓库、任务。
2. 可以通过 MCP Gateway 让 Main Codex 创建并调度任务。
3. Docker Executor 可以启动至少三类 Worker：feature、test、audit。
4. 每个 Worker 有独立 worktree 和 run directory。
5. Feature Worker 产出 diff 后必须执行 scope check。
6. scope violation 必须阻断后续流程。
7. Test Worker 和 Audit Worker 的结果必须进入 Task Detail。
8. Git Sync Worker 可以生成 PR body。
9. 所有 tool call、worker run、审批和最终决策必须写入审计日志。
10. Docker Compose 可以一键部署平台。
11. SSH Executor 至少完成技术预研和 PoC，第二阶段完整支持。

---

## 28. 参考资料

- Vite 官方站点与版本支持页：https://vite.dev/ ，https://vite.dev/releases
- Go 1.25 Release Notes：https://go.dev/doc/go1.25
- Go Release History：https://go.dev/doc/devel/release
- PostgreSQL 18 Release Notes：https://www.postgresql.org/docs/release/18.0/
- PostgreSQL Release Notes Archive：https://www.postgresql.org/docs/release/
- Docker Compose docs：https://docs.docker.com/compose/
- Docker Compose file reference：https://docs.docker.com/reference/compose-file/
- MCP introduction：https://modelcontextprotocol.io/docs/getting-started/intro
- MCP specification：https://modelcontextprotocol.io/specification/2025-06-18
- MCP tools specification：https://modelcontextprotocol.io/specification/2025-06-18/server/tools
- MCP authorization specification：https://modelcontextprotocol.io/specification/draft/basic/authorization
- Codex CLI MCP docs：https://developers.openai.com/codex/mcp
- Codex Skills docs：https://developers.openai.com/codex/skills
