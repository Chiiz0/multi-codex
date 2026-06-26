import { z } from "zod";

const authTokenKey = "multi-codex.authToken";
const apiTimeoutMs = 8_000;
const recordSchema = z.record(z.string(), z.unknown());

const projectSchema = z.object({
  id: z.string(),
  name: z.string(),
  slug: z.string(),
  description: z.string(),
  created_at: z.string()
});

const organizationSchema = z.object({
  id: z.string(),
  name: z.string(),
  slug: z.string(),
  created_at: z.string()
});

const repositorySchema = z.object({
  id: z.string(),
  project_id: z.string(),
  name: z.string(),
  provider: z.string(),
  remote_url: z.string(),
  default_branch: z.string(),
  local_mirror_path: z.string().optional(),
  created_at: z.string()
});

const taskSchema = z.object({
  id: z.string(),
  project_id: z.string(),
  repository_id: z.string(),
  task_key: z.string(),
  title: z.string(),
  status: z.string(),
  envelope: recordSchema,
  created_at: z.string(),
  updated_at: z.string()
});

const runSchema = z.object({
  id: z.string(),
  task_id: z.string(),
  role: z.string(),
  status: z.string(),
  executor: z.string(),
  executor_node_id: z.string().optional(),
  branch: z.string().optional(),
  worktree_path: z.string().optional(),
  result: recordSchema.optional(),
  created_at: z.string(),
  started_at: z.string().optional(),
  finished_at: z.string().optional()
});

const eventSchema = z.object({
  id: z.number(),
  run_id: z.string(),
  seq: z.number(),
  level: z.string(),
  event_type: z.string(),
  message: z.string(),
  payload: recordSchema,
  created_at: z.string()
});

const artifactSchema = z.object({
  id: z.string(),
  run_id: z.string(),
  kind: z.string(),
  name: z.string(),
  path: z.string(),
  sha256: z.string().optional(),
  size_bytes: z.number().optional(),
  metadata: recordSchema,
  created_at: z.string()
});

const artifactContentSchema = z.object({
  artifact: artifactSchema,
  content: z.string(),
  content_type: z.string(),
  truncated: z.boolean(),
  limit_bytes: z.number()
});

const auditLogSchema = z.object({
  id: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  action: z.string(),
  resource_type: z.string(),
  resource_id: z.string(),
  payload: recordSchema,
  prev_hash: z.string().optional(),
  entry_hash: z.string().optional(),
  created_at: z.string()
});

const skillSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string(),
  role: z.string(),
  enabled: z.boolean(),
  version: z.string().optional(),
  content_hash: z.string().optional(),
  path: z.string().optional(),
  created_at: z.string()
});

const skillVersionSchema = z.object({
  id: z.string(),
  skill_id: z.string(),
  version: z.string(),
  content_hash: z.string(),
  path: z.string(),
  created_at: z.string()
});

const agentProfileSchema = z.object({
  id: z.string(),
  project_id: z.string().optional(),
  name: z.string(),
  role: z.string(),
  model: z.string(),
  sandbox_mode: z.string(),
  approval_policy: z.string(),
  executor: z.string(),
  image: z.string().optional(),
  network_enabled: z.boolean(),
  config: recordSchema,
  created_at: z.string()
});

const executorNodeSchema = z.object({
  id: z.string(),
  kind: z.string(),
  name: z.string(),
  address: z.string().optional(),
  agentd_url: z.string().optional(),
  host_key_fingerprint: z.string().optional(),
  observed_host_key_fingerprint: z.string().optional(),
  host_key_verified: z.boolean(),
  forced_command: z.string().optional(),
  labels: recordSchema.optional(),
  capacity: recordSchema.optional(),
  status: z.string(),
  last_seen_at: z.string().optional(),
  verified_at: z.string().optional(),
  created_at: z.string().optional()
});

const nodeStateSchema = z.object({
  id: z.string(),
  name: z.string(),
  status: z.string(),
  eligible: z.boolean(),
  ineligible_reason: z.string().optional(),
  active_runs: z.number(),
  concurrency: z.number(),
  available_slots: z.number(),
  utilization: z.number(),
  selection_rank: z.number().optional(),
  selection_reason: z.string().optional()
});

const backpressureSchema = z.object({
  executor: z.string(),
  retry_after_seconds: z.number(),
  available_slots: z.number(),
  nodes: z.array(nodeStateSchema)
});

const approvalSchema = z.object({
  id: z.string(),
  task_id: z.string(),
  approval_type: z.string(),
  status: z.string(),
  reason: z.string(),
  requested_by: z.string().optional(),
  approved_by: z.string().optional(),
  created_at: z.string(),
  decided_at: z.string().optional()
});

const toolCallSchema = z.object({
  id: z.string(),
  run_id: z.string().optional(),
  caller: z.string(),
  tool_name: z.string(),
  input: recordSchema,
  output: recordSchema,
  status: z.string(),
  created_at: z.string(),
  finished_at: z.string().optional()
});

const workflowSchema = z.object({
  task: taskSchema,
  runs: z.array(runSchema),
  latest_scope_check: z
    .object({
      id: z.string(),
      task_id: z.string(),
      run_id: z.string().optional(),
      base_ref: z.string(),
      result: z.object({
        status: z.string(),
        changed_files: z.array(z.string()),
        violations: z.array(z.string())
      }),
      created_at: z.string()
    })
    .optional(),
  approvals: z.array(approvalSchema),
  blocked_reasons: z.array(z.string()),
  next_actions: z.array(z.string()),
  ready_for_pr: z.boolean()
});

const queueSnapshotSchema = z.object({
  queued_runs: z.array(runSchema),
  backpressure: z.record(z.string(), backpressureSchema)
});

const authContextSchema = z.object({
  user: z.object({
    id: z.string(),
    email: z.string(),
    display_name: z.string(),
    created_at: z.string()
  }),
  membership: z.object({
    org_id: z.string(),
    user_id: z.string(),
    role: z.string(),
    created_at: z.string()
  }),
  permissions: z.array(z.string())
});

const authCapabilitiesSchema = z.object({
  auth_mode: z.string(),
  oidc_configured: z.boolean(),
  session_ttl_seconds: z.number(),
  default_role: z.string()
});

export type Project = z.infer<typeof projectSchema>;
export type Organization = z.infer<typeof organizationSchema>;
export type Repository = z.infer<typeof repositorySchema>;
export type Task = z.infer<typeof taskSchema>;
export type Run = z.infer<typeof runSchema>;
export type RunEvent = z.infer<typeof eventSchema>;
export type Artifact = z.infer<typeof artifactSchema>;
export type ArtifactContent = z.infer<typeof artifactContentSchema>;
export type AuditLog = z.infer<typeof auditLogSchema>;
export type Skill = z.infer<typeof skillSchema>;
export type SkillVersion = z.infer<typeof skillVersionSchema>;
export type AgentProfile = z.infer<typeof agentProfileSchema>;
export type ExecutorNode = z.infer<typeof executorNodeSchema>;
export type Approval = z.infer<typeof approvalSchema>;
export type ToolCall = z.infer<typeof toolCallSchema>;
export type WorkflowState = z.infer<typeof workflowSchema>;
export type Backpressure = z.infer<typeof backpressureSchema>;
export type QueueSnapshot = z.infer<typeof queueSnapshotSchema>;
export type AuthContext = z.infer<typeof authContextSchema>;
export type AuthCapabilities = z.infer<typeof authCapabilitiesSchema>;

async function getJSON<T>(path: string, schema: z.ZodType<T>): Promise<T> {
  const response = await apiFetch(path, { credentials: "include", headers: authHeaders() });
  return parseResponse(response, schema);
}

async function postJSON<T>(path: string, body: unknown, schema: z.ZodType<T>): Promise<T> {
  const response = await apiFetch(path, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", ...authHeaders() },
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  return parseResponse(response, schema);
}

async function parseResponse<T>(response: Response, schema: z.ZodType<T>): Promise<T> {
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    const message = typeof payload?.error === "string" ? payload.error : `${response.status} ${response.statusText}`;
    throw new Error(message);
  }
  return schema.parse(payload);
}

async function apiFetch(input: RequestInfo | URL, init: RequestInit = {}) {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), apiTimeoutMs);
  try {
    return await fetch(input, { ...init, signal: init.signal ?? controller.signal });
  } finally {
    window.clearTimeout(timeout);
  }
}

export function getAuthContext() {
  return getJSON("/api/v1/auth/me", authContextSchema);
}

export function getAuthCapabilities() {
  return getJSON("/api/v1/auth/capabilities", authCapabilitiesSchema);
}

export function logout() {
  return postJSON("/api/v1/auth/logout", {}, z.object({ status: z.string(), mode: z.string() }));
}

export function beginOIDCLogin() {
  const returnTo = typeof window === "undefined" ? "/" : `${window.location.pathname}${window.location.search}${window.location.hash}` || "/";
  window.location.assign(`/api/v1/auth/login?return_to=${encodeURIComponent(returnTo)}`);
}

export async function createBrowserSession(token: string) {
  const response = await apiFetch("/api/v1/auth/session", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token.trim()}` },
    body: "{}"
  });
  return parseResponse(response, authContextSchema);
}

export function getAuthToken() {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(authTokenKey) ?? "";
}

export function setAuthToken(token: string) {
  if (typeof window === "undefined") {
    return;
  }
  const trimmed = token.trim();
  if (trimmed) {
    window.localStorage.setItem(authTokenKey, trimmed);
  } else {
    window.localStorage.removeItem(authTokenKey);
  }
}

export function clearAuthToken() {
  setAuthToken("");
}

function authHeaders(): Record<string, string> {
  const token = getAuthToken();
  return token ? { Authorization: `Bearer ${token}` } : {};
}

export function listProjects() {
  return getJSON("/api/v1/projects", z.array(projectSchema));
}

export function listOrganizations() {
  return getJSON("/api/v1/organizations", z.array(organizationSchema));
}

export function createOrganization(input: { name: string; slug: string }) {
  return postJSON("/api/v1/organizations", input, organizationSchema);
}

export function createProject(input: { name: string; slug: string; description: string }) {
  return postJSON("/api/v1/projects", input, projectSchema);
}

export function listRepositories(projectId: string) {
  return getJSON(`/api/v1/projects/${projectId}/repositories`, z.array(repositorySchema));
}

export function createRepository(projectId: string, input: { name: string; provider: string; remote_url: string; default_branch: string }) {
  return postJSON(`/api/v1/projects/${projectId}/repositories`, input, repositorySchema);
}

export function listTasks(projectId: string) {
  return getJSON(`/api/v1/projects/${projectId}/tasks`, z.array(taskSchema));
}

export function listRuns(taskId: string) {
  return getJSON(`/api/v1/tasks/${taskId}/runs`, z.array(runSchema));
}

export function listAllRuns() {
  return getJSON("/api/v1/runs", z.array(runSchema));
}

export function getQueueStatus() {
  return getJSON("/api/v1/queue", queueSnapshotSchema);
}

export function dispatchQueue() {
  return postJSON(
    "/api/v1/queue/dispatch",
    {},
    z.object({
      run: runSchema,
      queue: queueSnapshotSchema
    })
  );
}

export async function createTask(project: Project, repository: Repository, overrides: Partial<Record<string, unknown>> = {}) {
  const taskKey = String(overrides.task_id ?? `FEAT-${Date.now().toString().slice(-6)}`);
  const envelope = {
    task_id: taskKey,
    project_id: project.id,
    repository_id: repository.id,
    title: String(overrides.title ?? "Run governed worker lifecycle"),
    base_branch: String(overrides.base_branch ?? "origin/main"),
    target_branch: String(overrides.target_branch ?? `codex/${taskKey.toLowerCase()}/feature/local-executor`),
    role: String(overrides.role ?? "feature"),
    skill: String(overrides.skill ?? "company-feature-worker"),
    agent_profile: String(overrides.agent_profile ?? "feature-worker-go-node"),
    executor: String(overrides.executor ?? "docker"),
    allowed_paths: (overrides.allowed_paths as string[] | undefined) ?? ["internal/**", "apps/web/src/**", "docs/**"],
    forbidden_paths: (overrides.forbidden_paths as string[] | undefined) ?? [
      ".github/**",
      ".gitlab/**",
      "infra/**",
      "k8s/**",
      "terraform/**",
      "secrets/**",
      ".env*",
      "**/*secret*",
      "**/*credential*",
      "package-lock.json",
      "pnpm-lock.yaml",
      "go.sum"
    ],
    allowed_commands: (overrides.allowed_commands as string[] | undefined) ?? ["go test ./..."],
    network: Boolean(overrides.network ?? false),
    objective: String(overrides.objective ?? "Exercise the multi-codex worker lifecycle in the local development environment."),
    acceptance_criteria: (overrides.acceptance_criteria as string[] | undefined) ?? ["Task validates", "Worker run starts", "Run events are visible"],
    stop_conditions: (overrides.stop_conditions as string[] | undefined) ?? ["Need production credentials", "Need to push to a remote branch"],
    required_outputs: ["changed_files", "summary", "tests_run", "risks", "needs_human"],
    policy: {
      allow_push: false,
      allow_dependency_change: false,
      allow_infra_change: false,
      require_audit: true,
      require_tests: true,
      require_human_before_pr: true
    }
  };
  const payload = await postJSON(
    `/api/v1/projects/${project.id}/tasks`,
    { envelope },
    z.object({ task: taskSchema })
  );
  return payload.task;
}

export async function startTask(taskId: string) {
  const payload = await postJSON(`/api/v1/tasks/${taskId}/start`, undefined, z.object({ run: runSchema }));
  return payload.run;
}

export function getWorkflow(taskId: string) {
  return getJSON(`/api/v1/tasks/${taskId}/workflow`, workflowSchema);
}

export function runWorkflowAction(taskId: string, action: string) {
  return postJSON(`/api/v1/tasks/${taskId}/workflow/${action}`, {}, z.record(z.string(), z.unknown()));
}

export function listRunEvents(runId: string) {
  return getJSON(`/api/v1/runs/${runId}/events`, z.array(eventSchema));
}

export function runEventStreamURL(runId: string) {
  return `/api/v1/runs/${encodeURIComponent(runId)}/events/stream`;
}

export function parseRunEventPayload(payload: string): RunEvent | undefined {
  try {
    const decoded = eventSchema.safeParse(JSON.parse(payload));
    return decoded.success ? decoded.data : undefined;
  } catch {
    return undefined;
  }
}

export function listArtifacts(runId: string) {
  return getJSON(`/api/v1/runs/${runId}/artifacts`, z.array(artifactSchema));
}

export function getArtifactContent(artifactId: string) {
  return getJSON(`/api/v1/artifacts/${artifactId}/content`, artifactContentSchema);
}

export async function scopeCheck(taskId: string, changedFiles: string[]) {
  return postJSON(
    `/api/v1/tasks/${taskId}/scope-check`,
    { changed_files: changedFiles },
    z.object({
      status: z.string(),
      changed_files: z.array(z.string()),
      violations: z.array(z.string()),
      scope_check_id: z.string().optional()
    })
  );
}

export function listAuditLogs() {
  return getJSON("/api/v1/audit-logs", z.array(auditLogSchema));
}

export function listToolCalls() {
  return getJSON("/api/v1/tool-calls", z.array(toolCallSchema));
}

export function listSkills() {
  return getJSON("/api/v1/skills", z.array(skillSchema));
}

export function listSkillVersions(skillId: string) {
  return getJSON(`/api/v1/skills/${skillId}/versions`, z.array(skillVersionSchema));
}

export function createSkill(input: { name: string; role: string; description: string; version: string; path: string }) {
  return postJSON("/api/v1/skills", input, skillSchema);
}

export function listAgentProfiles(projectId: string) {
  return getJSON(`/api/v1/projects/${projectId}/agent-profiles`, z.array(agentProfileSchema));
}

export function createAgentProfile(projectId: string, input: Omit<AgentProfile, "id" | "project_id" | "created_at">) {
  return postJSON(`/api/v1/projects/${projectId}/agent-profiles`, input, agentProfileSchema);
}

export function listExecutorNodes() {
  return getJSON("/api/v1/executor-nodes", z.array(executorNodeSchema));
}

export function registerExecutorNode(input: { kind: string; name: string; address: string; status: string; agentd_url?: string; host_key_fingerprint?: string; forced_command?: string }) {
  return postJSON("/api/v1/executor-nodes", { ...input, labels: {}, capacity: {} }, executorNodeSchema);
}

export function verifyExecutorNodeHostKey(nodeId: string, observedFingerprint: string) {
  return postJSON(`/api/v1/executor-nodes/${nodeId}/verify-host-key`, { observed_fingerprint: observedFingerprint }, executorNodeSchema);
}

export function listApprovals() {
  return getJSON("/api/v1/approvals", z.array(approvalSchema));
}

export function requestApproval(taskId: string, input: { approval_type: string; reason: string }) {
  return postJSON(`/api/v1/tasks/${taskId}/approvals`, input, approvalSchema);
}

export function decideApproval(approvalId: string, status: "approved" | "rejected", reason: string) {
  return postJSON(`/api/v1/approvals/${approvalId}/decision`, { status, reason }, approvalSchema);
}
