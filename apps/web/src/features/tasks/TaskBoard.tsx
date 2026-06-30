import { FormEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { queryClient } from "../../app/queryClient";
import { StatusBadge } from "../../components/StatusBadge";
import {
  Backpressure,
  beginOIDCLogin,
  clearAuthToken,
  createAgentProfile,
  createBrowserSession,
  createOrganization,
  createProject,
  createRepository,
  createSkill,
  createUser,
  createTask,
  decideApproval,
  dispatchQueue,
  getArtifactContent,
  getAuthCapabilities,
  getAuthContext,
  getAuthToken,
  getQueueStatus,
  getWorkflow,
  listAgentProfiles,
  listAllRuns,
  listApprovals,
  listArtifacts,
  listAuditLogs,
  listExecutorNodes,
  listOrganizations,
  listProjectMembers,
  listProjects,
  listRepositories,
  listRunEvents,
  listRuns,
  listSkillVersions,
  listSkills,
  listTasks,
  listToolCalls,
  listUsers,
  loginWithPassword,
  logout,
  parseRunEventPayload,
  registerExecutorNode,
  requestApproval,
  runEventStreamURL,
  runWorkflowAction,
  scopeCheck,
  startTask,
  setAuthToken,
  upsertProjectMember,
  verifyExecutorNodeHostKey
} from "../../lib/api";
import type {
  AgentProfile,
  Approval,
  AuditLog,
  AuthCapabilities,
  AuthContext,
  Organization,
  Project,
  ProjectMembership,
  QueueSnapshot,
  Repository,
  Run,
  RunEvent,
  SkillVersion,
  Task,
  ToolCall,
  UserDirectory
} from "../../lib/api";
import { LanguageToggle, useI18n } from "../../lib/i18n";
import { AccessProvider, hasPermission, projectRole, useAccess, visiblePermissions } from "../../lib/permissions";
import type { Permission } from "../../lib/permissions";

type View = "dashboard" | "tasks" | "runs" | "queue" | "approvals" | "nodes" | "organizations" | "skills" | "admin" | "audit";
type ListLimit = 10 | 20 | 50;
type ListPager = {
  hasNext: boolean;
  hasPrevious: boolean;
  limit: ListLimit;
  page: number;
  pageCount: number;
  setLimit: (value: ListLimit) => void;
  goNext: () => void;
  goPrevious: () => void;
  total: number;
};
type ListPagination<T> = ListPager & { items: T[] };

const listLimitOptions = [10, 20, 50] as const;

const navItems: Array<{ id: View; labelKey: string; permission: Permission }> = [
  { id: "dashboard", labelKey: "nav.dashboard", permission: "projects:read" },
  { id: "tasks", labelKey: "nav.tasks", permission: "projects:read" },
  { id: "runs", labelKey: "nav.runs", permission: "runs:read" },
  { id: "queue", labelKey: "nav.queue", permission: "runs:read" },
  { id: "approvals", labelKey: "nav.approvals", permission: "projects:read" },
  { id: "nodes", labelKey: "nav.nodes", permission: "nodes:read" },
  { id: "organizations", labelKey: "nav.organizations", permission: "organizations:read" },
  { id: "skills", labelKey: "nav.skills", permission: "projects:read" },
  { id: "admin", labelKey: "nav.admin", permission: "users:read" },
  { id: "audit", labelKey: "nav.audit", permission: "audit:read" }
];

function ListLimitControl({
  label,
  onChange,
  value
}: {
  label?: string;
  onChange: (value: ListLimit) => void;
  value: ListLimit;
}) {
  const { t } = useI18n();
  return (
    <div className="list-limit-control" role="group" aria-label={label ?? t("common.displayCount")}>
      <span>{t("common.show")}</span>
      {listLimitOptions.map((option) => (
        <button
          aria-label={t("common.rows", { count: option })}
          aria-pressed={value === option}
          className={value === option ? "active" : ""}
          key={option}
          onClick={() => onChange(option)}
          type="button"
        >
          {option}
        </button>
      ))}
    </div>
  );
}

function ListPaginationControl({ label, pagination }: { label?: string; pagination: ListPager }) {
  const { t } = useI18n();
  return (
    <div className="list-pagination-control" role="group" aria-label={label ?? t("common.pagination")}>
      <ListLimitControl label={label} value={pagination.limit} onChange={pagination.setLimit} />
      <div className="pager-control">
        <button
          aria-label={t("common.previousPage")}
          disabled={!pagination.hasPrevious}
          onClick={pagination.goPrevious}
          type="button"
        >
          {t("common.prev")}
        </button>
        <span>{t("common.pageStatus", { page: pagination.page, pages: pagination.pageCount })}</span>
        <button aria-label={t("common.nextPage")} disabled={!pagination.hasNext} onClick={pagination.goNext} type="button">
          {t("common.next")}
        </button>
      </div>
    </div>
  );
}

function useListPagination<T>(items: T[], options: { fromEnd?: boolean } = {}): ListPagination<T> {
  const [limit, setLimitState] = useState<ListLimit>(10);
  const [page, setPage] = useState(1);
  const total = items.length;
  const pageCount = Math.max(1, Math.ceil(total / limit));
  const normalizedPage = Math.min(page, pageCount);

  useEffect(() => {
    setPage((current) => Math.min(Math.max(current, 1), pageCount));
  }, [pageCount]);

  const visibleItems = useMemo(() => {
    if (!total) {
      return [];
    }
    if (options.fromEnd) {
      const end = Math.max(total - (normalizedPage - 1) * limit, 0);
      const start = Math.max(end - limit, 0);
      return items.slice(start, end);
    }
    const start = (normalizedPage - 1) * limit;
    return items.slice(start, start + limit);
  }, [items, limit, normalizedPage, options.fromEnd, total]);

  return {
    hasNext: normalizedPage < pageCount,
    hasPrevious: normalizedPage > 1,
    items: visibleItems,
    limit,
    page: normalizedPage,
    pageCount,
    setLimit: (nextLimit: ListLimit) => {
      setLimitState(nextLimit);
      setPage(1);
    },
    goNext: () => setPage((current) => Math.min(current + 1, pageCount)),
    goPrevious: () => setPage((current) => Math.max(current - 1, 1)),
    total
  };
}

export function TaskBoard() {
  const { t } = useI18n();
  const [view, setView] = useHashView();
  const [path, setPath] = useState(() => window.location.pathname);
  const [selectedProjectId, setSelectedProjectId] = useState<string | undefined>();
  const [selectedRepositoryId, setSelectedRepositoryId] = useState<string | undefined>();
  const [selectedTaskId, setSelectedTaskId] = useState<string | undefined>();
  const [tokenDraft, setTokenDraft] = useState(getAuthToken);
  const [loginEmail, setLoginEmail] = useState("local-dev@multi-codex.invalid");
  const [loginPassword, setLoginPassword] = useState("admin123");
  const capabilities = useQuery({ queryKey: ["auth-capabilities"], queryFn: getAuthCapabilities, staleTime: 60_000 });
  const auth = useQuery({ queryKey: ["auth"], queryFn: getAuthContext, retry: false });
  const isAuthenticated = Boolean(auth.data);
  const isLoginRoute = path === "/login";
  const canProjectsRead = isAuthenticated && hasPermission(auth.data, "projects:read");
  const canOrganizationsRead = isAuthenticated && hasPermission(auth.data, "organizations:read");
  const canRunsRead = isAuthenticated && hasPermission(auth.data, "runs:read");
  const canUsersRead = isAuthenticated && hasPermission(auth.data, "users:read");
  const canAuditRead = isAuthenticated && hasPermission(auth.data, "audit:read");
  const navigate = (nextPath: string) => {
    if (window.location.pathname !== nextPath) {
      window.history.pushState(null, "", nextPath);
    }
    setPath(window.location.pathname);
  };
  const logoutMutation = useMutation({
    mutationFn: logout,
    onSettled: () => {
      clearAuthToken();
      setTokenDraft("");
      queryClient.invalidateQueries();
      navigate("/login");
    }
  });
  const connectMutation = useMutation({
    mutationFn: createBrowserSession,
    onSuccess: (nextAuth) => {
      clearAuthToken();
      setTokenDraft("");
      queryClient.setQueryData(["auth"], nextAuth);
      queryClient.invalidateQueries();
      navigate("/");
    }
  });
  const passwordLoginMutation = useMutation({
    mutationFn: loginWithPassword,
    onSuccess: (nextAuth) => {
      clearAuthToken();
      setTokenDraft("");
      queryClient.setQueryData(["auth"], nextAuth);
      queryClient.invalidateQueries();
      navigate("/");
    }
  });
  const organizations = useQuery({ queryKey: ["organizations"], queryFn: listOrganizations, enabled: canOrganizationsRead });
  const projects = useQuery({ queryKey: ["projects"], queryFn: listProjects, enabled: canProjectsRead });
  const users = useQuery({ queryKey: ["users"], queryFn: listUsers, enabled: canUsersRead });
  const activeProject = useMemo(() => {
    if (!projects.data?.length) {
      return undefined;
    }
    return projects.data.find((project) => project.id === selectedProjectId) ?? projects.data[0];
  }, [projects.data, selectedProjectId]);
  const repositories = useQuery({
    queryKey: ["repositories", activeProject?.id],
    queryFn: () => listRepositories(activeProject!.id),
    enabled: canProjectsRead && Boolean(activeProject)
  });
  const tasks = useQuery({
    queryKey: ["tasks", activeProject?.id],
    queryFn: () => listTasks(activeProject!.id),
    enabled: canProjectsRead && Boolean(activeProject),
    refetchInterval: pollWhileHealthy(2_000)
  });
  const runs = useQuery({ queryKey: ["runs"], queryFn: listAllRuns, enabled: canRunsRead, refetchInterval: pollWhileHealthy(2_000) });
  const queue = useQuery({ queryKey: ["queue"], queryFn: getQueueStatus, enabled: canRunsRead, refetchInterval: pollWhileHealthy(2_000) });
  const approvals = useQuery({ queryKey: ["approvals"], queryFn: listApprovals, enabled: canProjectsRead, refetchInterval: pollWhileHealthy(4_000) });
  const auditLogs = useQuery({ queryKey: ["audit-logs"], queryFn: listAuditLogs, enabled: canAuditRead, refetchInterval: pollWhileHealthy(5_000) });
  const toolCalls = useQuery({ queryKey: ["tool-calls"], queryFn: listToolCalls, enabled: canAuditRead, refetchInterval: pollWhileHealthy(5_000) });

  const activeRepository = useMemo(() => {
    if (!repositories.data?.length) {
      return undefined;
    }
    return repositories.data.find((repository) => repository.id === selectedRepositoryId) ?? repositories.data[0];
  }, [repositories.data, selectedRepositoryId]);
  const selectedTask = useMemo(() => {
    if (!tasks.data?.length) {
      return undefined;
    }
    return tasks.data.find((task) => task.id === selectedTaskId) ?? tasks.data[0];
  }, [selectedTaskId, tasks.data]);

  useEffect(() => {
    if (!selectedProjectId && projects.data?.[0]) {
      setSelectedProjectId(projects.data[0].id);
    }
  }, [projects.data, selectedProjectId]);

  useEffect(() => {
    if (!selectedRepositoryId && repositories.data?.[0]) {
      setSelectedRepositoryId(repositories.data[0].id);
    }
  }, [repositories.data, selectedRepositoryId]);

  useEffect(() => {
    const onPopState = () => setPath(window.location.pathname);
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  useEffect(() => {
    if (!auth.data && capabilities.data?.auth_mode !== "oidc" && capabilities.data?.local_admin_email) {
      setLoginEmail((current) => (current === "local-dev@multi-codex.invalid" ? capabilities.data!.local_admin_email! : current));
    }
  }, [auth.data, capabilities.data]);

  useEffect(() => {
    if (auth.data && isLoginRoute) {
      navigate("/");
    }
    if (auth.isError && !isLoginRoute) {
      navigate("/login");
    }
  }, [auth.data, auth.isError, isLoginRoute]);

  const activeNavItem = navItems.find((item) => item.id === view) ?? navItems[0];
  const canViewActive = isAuthenticated && hasPermission(auth.data, activeNavItem.permission);
  const activeProjectRole = projectRole(auth.data, activeProject?.id);
  const saveToken = () => {
    const trimmed = tokenDraft.trim();
    if (trimmed) {
      connectMutation.mutate(trimmed);
    } else {
      setAuthToken("");
      queryClient.invalidateQueries();
    }
  };

  return (
    <AccessProvider auth={auth.data}>
    {isLoginRoute ? (
      <LoginPage
        auth={auth.data}
        authError={passwordLoginMutation.error instanceof Error ? passwordLoginMutation.error : null}
        capabilities={capabilities.data}
        isConnecting={connectMutation.isPending}
        isPasswordLoginPending={passwordLoginMutation.isPending}
        loginEmail={loginEmail}
        loginPassword={loginPassword}
        onLoginEmailChange={setLoginEmail}
        onLoginPasswordChange={setLoginPassword}
        onOIDCLogin={beginOIDCLogin}
        onPasswordLogin={() => passwordLoginMutation.mutate({ email: loginEmail, password: loginPassword })}
        onOpenConsole={() => navigate("/")}
        onSaveToken={saveToken}
        tokenDraft={tokenDraft}
        onTokenDraftChange={setTokenDraft}
      />
    ) : (
    <main className="shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">mcx</span>
          <div>
            <h1>multi-codex</h1>
            <p>
              {auth.data
                ? t("auth.sessionUserRole", { user: auth.data.user.display_name, role: roleLabel(auth.data.membership.role, t) })
                : t("app.subtitle")}
            </p>
          </div>
        </div>
        <nav className="nav-list" aria-label={t("nav.primary")}>
          {navItems.map((item) => {
            const navAllowed = isAuthenticated && hasPermission(auth.data, item.permission);
            return (
            <a
              key={item.id}
              aria-disabled={!navAllowed}
              className={`${view === item.id ? "active" : ""} ${!navAllowed ? "locked" : ""}`}
              href={`#${item.id}`}
              onClick={() => setView(item.id)}
              title={navAllowed ? undefined : t("access.missing", { permission: item.permission })}
            >
              <span>{t(item.labelKey)}</span>
              {!navAllowed ? <small>{t("access.navLocked")}</small> : null}
            </a>
            );
          })}
        </nav>
        <AuthControls
          auth={auth.data}
          capabilities={capabilities.data}
          authError={auth.error}
          isConnecting={connectMutation.isPending}
          isLoggingOut={logoutMutation.isPending}
          onOIDCLogin={beginOIDCLogin}
          onLogout={() => logoutMutation.mutate()}
          onSaveToken={saveToken}
          tokenDraft={tokenDraft}
          onTokenDraftChange={setTokenDraft}
        />
      </aside>

      <section className="workspace">
        <header className="topbar">
          <div className="topbar-selectors">
            <label>
              <span>{t("topbar.project")}</span>
              <select
                className="title-select"
                value={activeProject?.id ?? ""}
                onChange={(event) => {
                  setSelectedProjectId(event.target.value);
                  setSelectedRepositoryId(undefined);
                }}
              >
                {projects.data?.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>{t("topbar.repository")}</span>
              <select
                className="title-select"
                value={activeRepository?.id ?? ""}
                onChange={(event) => setSelectedRepositoryId(event.target.value)}
              >
                {repositories.data?.map((repository) => (
                  <option key={repository.id} value={repository.id}>
                    {repository.name}
                  </option>
                ))}
              </select>
            </label>
            <div className="branch-pill">
              <span>{activeProjectRole ? t("topbar.projectRole") : t("topbar.branch")}</span>
              <strong>{activeProjectRole ? roleLabel(activeProjectRole, t) : activeRepository?.default_branch || "main"}</strong>
            </div>
          </div>
          <div className="system-strip">
            <StatusBadge status={auth.data ? "operational" : auth.isError ? "auth_required" : "checking"} />
            <button className="secondary-button compact" type="button" onClick={() => queryClient.invalidateQueries()}>
              {t("topbar.refresh")}
            </button>
          </div>
        </header>

        {auth.data ? (
          <section className="summary-strip" aria-label={t("summary.workspace")}>
            <Metric label={t("summary.projects")} value={projects.data?.length ?? 0} />
            <Metric label={t("summary.tasks")} value={tasks.data?.length ?? 0} />
            <Metric label={t("summary.runs")} value={runs.data?.length ?? 0} />
            <Metric label={t("summary.queued")} value={queue.data?.queued_runs.length ?? 0} />
            <Metric label={t("summary.users")} value={users.data?.users.length ?? 0} />
          </section>
        ) : null}

        <div className="view-host">
          {!auth.data ? (
            <AccessPanel permission="auth:session" />
          ) : !canViewActive ? (
            <AccessPanel permission={activeNavItem.permission} />
          ) : view === "dashboard" ? (
            <DashboardView
              activeProject={activeProject}
              activeProjectId={activeProject?.id}
              activeRepository={activeRepository}
              approvals={approvals.data ?? []}
              auditLogs={auditLogs.data ?? []}
              onSelectTask={setSelectedTaskId}
              onSelectView={setView}
              queue={queue.data}
              runs={runs.data ?? []}
              selectedTask={selectedTask}
              tasks={tasks.data ?? []}
              toolCalls={toolCalls.data ?? []}
            />
          ) : null}

          {auth.data && canViewActive && view === "tasks" ? (
            <TasksView
              activeProject={activeProject}
              activeRepository={activeRepository}
              onSelectTask={setSelectedTaskId}
              selectedTask={selectedTask}
              tasks={tasks.data ?? []}
            />
          ) : null}

          {auth.data && canViewActive && view === "runs" ? <RunsView /> : null}
          {auth.data && canViewActive && view === "queue" ? <QueueView /> : null}
          {auth.data && canViewActive && view === "approvals" ? <ApprovalsView /> : null}
          {auth.data && canViewActive && view === "nodes" ? <NodesView /> : null}
          {auth.data && canViewActive && view === "organizations" ? <OrganizationsView organizations={organizations.data ?? []} /> : null}
          {auth.data && canViewActive && view === "skills" ? <SkillsView projectId={activeProject?.id} /> : null}
          {auth.data && canViewActive && view === "admin" ? (
            <AdminView
              activeProject={activeProject}
              onSelectProject={setSelectedProjectId}
              projects={projects.data ?? []}
              users={users.data}
            />
          ) : null}
          {auth.data && canViewActive && view === "audit" ? <AuditView /> : null}
        </div>
      </section>
    </main>
    )}
    </AccessProvider>
  );
}

function AuthControls({
  auth,
  capabilities,
  authError,
  isConnecting,
  isLoggingOut,
  onLogout,
  onOIDCLogin,
  onSaveToken,
  tokenDraft,
  onTokenDraftChange
}: {
  auth?: AuthContext;
  capabilities?: AuthCapabilities;
  authError: Error | null;
  isConnecting: boolean;
  isLoggingOut: boolean;
  onLogout: () => void;
  onOIDCLogin: () => void;
  onSaveToken: () => void;
  tokenDraft: string;
  onTokenDraftChange: (value: string) => void;
}) {
  const { t } = useI18n();
  const permissions = visiblePermissions(auth);
  const sessionLabel = auth
    ? t("auth.sessionUserRole", { user: auth.user.email, role: roleLabel(auth.membership.role, t) })
    : authError
      ? t("auth.required")
      : t("auth.checking");
  const authMode = capabilities ? (capabilities.auth_mode === "oidc" ? t("auth.oidc") : t("auth.local")) : t("auth.checking");
  const isOIDC = capabilities?.auth_mode === "oidc";
  return (
    <div className="auth-controls">
      <LanguageToggle />
      <div className="auth-status">
        <span>{t("auth.session")}</span>
        <strong>{sessionLabel}</strong>
      </div>
      <div className="auth-status">
        <span>{t("auth.mode")}</span>
        <strong>{authMode}</strong>
      </div>
      {auth ? (
        <div className="permission-stack" aria-label={t("auth.permissions")}>
          {permissions.slice(0, 5).map((permission) => (
            <code key={permission}>{permission === "*" ? t("auth.allPermissions") : permission}</code>
          ))}
          {permissions.length > 5 ? <code>+{permissions.length - 5}</code> : null}
        </div>
      ) : null}
      {isOIDC ? (
        <input
          aria-label={t("auth.bearerToken")}
          placeholder={t("auth.bearerToken")}
          type="password"
          value={tokenDraft}
          onChange={(event) => onTokenDraftChange(event.target.value)}
        />
      ) : null}
      <div className="auth-actions">
        {isOIDC ? (
          <>
            <button className="secondary-button" type="button" onClick={onOIDCLogin}>
              {t("auth.signIn")}
            </button>
            <button className="secondary-button" type="button" disabled={isConnecting} onClick={onSaveToken}>
              {isConnecting ? t("auth.connecting") : t("auth.connect")}
            </button>
          </>
        ) : null}
        <button className="secondary-button" type="button" disabled={isLoggingOut} onClick={onLogout}>
          {t("auth.signOut")}
        </button>
      </div>
    </div>
  );
}

function LoginPage({
  auth,
  capabilities,
  authError,
  isConnecting,
  isPasswordLoginPending,
  loginEmail,
  loginPassword,
  onLoginEmailChange,
  onLoginPasswordChange,
  onPasswordLogin,
  onOIDCLogin,
  onOpenConsole,
  onSaveToken,
  tokenDraft,
  onTokenDraftChange
}: {
  auth?: AuthContext;
  capabilities?: AuthCapabilities;
  authError: Error | null;
  isConnecting: boolean;
  isPasswordLoginPending: boolean;
  loginEmail: string;
  loginPassword: string;
  onLoginEmailChange: (value: string) => void;
  onLoginPasswordChange: (value: string) => void;
  onPasswordLogin: () => void;
  onOIDCLogin: () => void;
  onOpenConsole: () => void;
  onSaveToken: () => void;
  tokenDraft: string;
  onTokenDraftChange: (value: string) => void;
}) {
  const { t } = useI18n();
  return (
    <main className="login-page">
      <section className="login-hero">
        <div className="login-brand">
          <span className="brand-mark">mcx</span>
          <div>
            <h1>multi-codex</h1>
            <p>{t("auth.enterpriseSubtitle")}</p>
          </div>
        </div>
        <div className="login-copy">
          <p className="eyebrow">{t("auth.secureWorkspace")}</p>
          <h2>{t("auth.loginHeroTitle")}</h2>
          <p>{t("auth.loginHeroBody")}</p>
        </div>
        <div className="login-assurance">
          <span>{t("auth.assuranceEnvelope")}</span>
          <span>{t("auth.assuranceRbac")}</span>
          <span>{t("auth.assuranceAudit")}</span>
        </div>
      </section>
      <LoginPanel
        auth={auth}
        authError={authError}
        capabilities={capabilities}
        isConnecting={isConnecting}
        isPasswordLoginPending={isPasswordLoginPending}
        loginEmail={loginEmail}
        loginPassword={loginPassword}
        onLoginEmailChange={onLoginEmailChange}
        onLoginPasswordChange={onLoginPasswordChange}
        onPasswordLogin={onPasswordLogin}
        onOIDCLogin={onOIDCLogin}
        onOpenConsole={onOpenConsole}
        onSaveToken={onSaveToken}
        onTokenDraftChange={onTokenDraftChange}
        tokenDraft={tokenDraft}
      />
    </main>
  );
}

function LoginPanel({
  auth,
  capabilities,
  authError,
  isConnecting,
  isPasswordLoginPending,
  loginEmail,
  loginPassword,
  onLoginEmailChange,
  onLoginPasswordChange,
  onPasswordLogin,
  onOIDCLogin,
  onOpenConsole,
  onSaveToken,
  tokenDraft,
  onTokenDraftChange
}: {
  auth?: AuthContext;
  capabilities?: AuthCapabilities;
  authError: Error | null;
  isConnecting: boolean;
  isPasswordLoginPending: boolean;
  loginEmail: string;
  loginPassword: string;
  onLoginEmailChange: (value: string) => void;
  onLoginPasswordChange: (value: string) => void;
  onPasswordLogin: () => void;
  onOIDCLogin: () => void;
  onOpenConsole?: () => void;
  onSaveToken: () => void;
  tokenDraft: string;
  onTokenDraftChange: (value: string) => void;
}) {
  const { t } = useI18n();
  const isOIDC = capabilities?.auth_mode === "oidc";
  const helperText = !capabilities
    ? t("auth.apiUnavailable")
    : isOIDC
    ? capabilities?.oidc_configured
      ? t("auth.loginOidcReady")
      : t("auth.loginOidcMissing")
    : t("auth.loginLocal");
  const ttlHours = capabilities?.session_ttl_seconds ? Math.round(capabilities.session_ttl_seconds / 3600) : undefined;

  return (
    <section className="login-panel panel">
      <div>
        <p className="eyebrow">{t("auth.session")}</p>
        <h2>{t("auth.loginTitle")}</h2>
        <p>{t("auth.loginBody")}</p>
      </div>
      <div className="login-meta">
        <span>{helperText}</span>
        {ttlHours ? <code>{t("auth.sessionTtl", { hours: ttlHours })}</code> : null}
        {capabilities?.default_role ? <code>{t("auth.defaultRole", { role: roleLabel(capabilities.default_role, t) })}</code> : null}
        {authError ? <code>{authError.message}</code> : null}
      </div>
      <div className="login-actions">
        {auth ? (
          <button className="primary-button" type="button" onClick={onOpenConsole}>
            {t("auth.openConsole")}
          </button>
        ) : !isOIDC ? (
          <form className="credential-login" onSubmit={(event) => submit(event, onPasswordLogin)}>
            <label>
              {t("auth.email")}
              <input
                aria-label={t("auth.email")}
                autoComplete="username"
                value={loginEmail}
                onChange={(event) => onLoginEmailChange(event.target.value)}
                placeholder={t("auth.emailPlaceholder")}
              />
            </label>
            <label>
              {t("auth.password")}
              <input
                aria-label={t("auth.password")}
                autoComplete="current-password"
                type="password"
                value={loginPassword}
                onChange={(event) => onLoginPasswordChange(event.target.value)}
                placeholder={t("auth.passwordPlaceholder")}
              />
            </label>
            <button className="primary-button" type="submit" disabled={isPasswordLoginPending}>
              {isPasswordLoginPending ? t("auth.connecting") : t("auth.signIn")}
            </button>
          </form>
        ) : (
          <button className="primary-button" type="button" onClick={onOIDCLogin}>
            {t("auth.signIn")}
          </button>
        )}
        {isOIDC ? (
          <details className="advanced-login">
            <summary>{t("auth.advancedLogin")}</summary>
            <div className="token-exchange">
              <label>
                {t("auth.bearerToken")}
                <input
                  aria-label={t("auth.bearerToken")}
                  type="password"
                  value={tokenDraft}
                  onChange={(event) => onTokenDraftChange(event.target.value)}
                  placeholder={t("auth.tokenHelp")}
                />
              </label>
              <button className="secondary-button" type="button" disabled={isConnecting} onClick={onSaveToken}>
                {isConnecting ? t("auth.connecting") : t("auth.connect")}
              </button>
            </div>
          </details>
        ) : null}
      </div>
    </section>
  );
}

function AccessPanel({ permission }: { permission: Permission | string }) {
  const { t } = useI18n();
  return (
    <section className="access-panel panel">
      <p className="eyebrow">{t("access.lockedTitle")}</p>
      <h2>{t("access.lockedTitle")}</h2>
      <p>{t("access.lockedBody")}</p>
      <code>{t("access.missing", { permission })}</code>
    </section>
  );
}

function AccessNotice({ permission }: { permission: Permission | string }) {
  const { t } = useI18n();
  return (
    <div className="access-notice">
      <span>{t("access.writeLocked")}</span>
      <code>{t("access.missing", { permission })}</code>
    </div>
  );
}

function useHashView(): [View, (view: View) => void] {
  const readHash = () => normalizeView(window.location.hash.replace("#", ""));
  const [view, setViewState] = useState<View>(readHash);

  useEffect(() => {
    const onHashChange = () => setViewState(readHash());
    window.addEventListener("hashchange", onHashChange);
    if (!window.location.hash) {
      window.history.replaceState(null, "", "#dashboard");
    }
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  const setView = (nextView: View) => {
    setViewState(nextView);
    if (window.location.hash !== `#${nextView}`) {
      window.location.hash = nextView;
    }
  };

  return [view, setView];
}

function normalizeView(value: string): View {
  return navItems.some((item) => item.id === value) ? (value as View) : "dashboard";
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function DashboardView({
  activeProject,
  activeProjectId,
  activeRepository,
  approvals,
  auditLogs,
  onSelectTask,
  onSelectView,
  queue,
  runs,
  selectedTask,
  tasks,
  toolCalls
}: {
  activeProject?: Project;
  activeProjectId?: string;
  activeRepository?: Repository;
  approvals: Approval[];
  auditLogs: AuditLog[];
  onSelectTask: (taskId: string) => void;
  onSelectView: (view: View) => void;
  queue?: QueueSnapshot;
  runs: Run[];
  selectedTask?: Task;
  tasks: Task[];
  toolCalls: ToolCall[];
}) {
  const selectedRuns = useMemo(() => runs.filter((run) => run.task_id === selectedTask?.id), [runs, selectedTask?.id]);
  const latestRun = selectedRuns[selectedRuns.length - 1];

  return (
    <section className="cockpit-grid">
      <div className="cockpit-stack">
        <ActiveTasksPanel onSelectTask={onSelectTask} selectedTask={selectedTask} tasks={tasks} />
        <QueueHealthCard onSelectView={onSelectView} queue={queue} runs={runs} />
        {!activeProject || !activeRepository ? <ProjectRepoPanel activeProjectId={activeProjectId} /> : null}
      </div>

      <TaskLifecyclePanel
        activeProject={activeProject}
        activeRepository={activeRepository}
        approvals={approvals}
        latestRun={latestRun}
        onSelectView={onSelectView}
        runs={selectedRuns}
        task={selectedTask}
      />

      <EvidenceColumn auditLogs={auditLogs} latestRun={latestRun} onSelectView={onSelectView} toolCalls={toolCalls} />
    </section>
  );
}

type TaskFilter = "all" | "queued" | "running" | "blocked" | "done";

function ActiveTasksPanel({
  onSelectTask,
  selectedTask,
  tasks
}: {
  onSelectTask: (taskId: string) => void;
  selectedTask?: Task;
  tasks: Task[];
}) {
  const { t } = useI18n();
  const [filter, setFilter] = useState<TaskFilter>("all");
  const buckets = useMemo(
    () => ({
      all: tasks.length,
      queued: tasks.filter((task) => ["queued", "pending", "created"].includes(task.status)).length,
      running: tasks.filter((task) => ["running", "started", "validating"].includes(task.status)).length,
      blocked: tasks.filter((task) => ["blocked", "failed", "rejected"].includes(task.status)).length,
      done: tasks.filter((task) => ["done", "completed", "succeeded", "approved"].includes(task.status)).length
    }),
    [tasks]
  );
  const visibleTasks = useMemo(() => {
    const sorted = [...tasks].sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime());
    if (filter === "all") {
      return sorted;
    }
    return sorted.filter((task) => {
      if (filter === "queued") {
        return ["queued", "pending", "created"].includes(task.status);
      }
      if (filter === "running") {
        return ["running", "started", "validating"].includes(task.status);
      }
      if (filter === "blocked") {
        return ["blocked", "failed", "rejected"].includes(task.status);
      }
      return ["done", "completed", "succeeded", "approved"].includes(task.status);
    });
  }, [filter, tasks]);
  const pagination = useListPagination(visibleTasks);

  return (
    <section className="panel cockpit-panel active-tasks-panel">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">{t("dashboard.workQueue")}</p>
          <h3>{t("dashboard.activeTasks")}</h3>
        </div>
        <div className="panel-heading-actions">
          <span>{t("common.total", { count: tasks.length })}</span>
          <ListPaginationControl pagination={pagination} />
        </div>
      </div>
      <div className="segment-group" role="tablist" aria-label={t("tasks.filters")}>
        {(["all", "queued", "running", "blocked", "done"] as TaskFilter[]).map((item) => (
          <button
            aria-selected={filter === item}
            className={`segment-button ${filter === item ? "active" : ""}`}
            key={item}
            onClick={() => setFilter(item)}
            type="button"
          >
            {t(`filters.${item}`)}
            <strong>{buckets[item]}</strong>
          </button>
        ))}
      </div>
      <div className="task-list cockpit-task-list dashboard-list">
        {visibleTasks.length ? (
          pagination.items.map((task) => (
            <TaskRow key={task.id} task={task} active={task.id === selectedTask?.id} onSelect={() => onSelectTask(task.id)} />
          ))
        ) : (
          <div className="empty-state">{t("tasks.noFilterMatch")}</div>
        )}
      </div>
    </section>
  );
}

function QueueHealthCard({ onSelectView, queue, runs }: { onSelectView: (view: View) => void; queue?: QueueSnapshot; runs: Run[] }) {
  const { t } = useI18n();
  const access = useAccess();
  const dispatchMutation = useMutation({
    mutationFn: dispatchQueue,
    onSuccess: (payload) => {
      void queryClient.invalidateQueries({ queryKey: ["queue"] });
      void queryClient.invalidateQueries({ queryKey: ["runs"] });
      void queryClient.invalidateQueries({ queryKey: ["tasks"] });
      void queryClient.invalidateQueries({ queryKey: ["run-events", payload.run.id] });
    }
  });
  const running = runs.filter((run) => run.status === "running").length;
  const queued = queue?.queued_runs.length ?? 0;
  const blocked = runs.filter((run) => ["blocked", "failed"].includes(run.status)).length;
  const completed = runs.filter((run) => ["completed", "succeeded"].includes(run.status)).length;
  const snapshots = Object.values(queue?.backpressure ?? {});
  const pagination = useListPagination(snapshots);

  return (
    <section className="panel cockpit-panel">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">{t("dashboard.capacity")}</p>
          <h3>{t("dashboard.queueHealth")}</h3>
        </div>
        <div className="panel-heading-actions">
          <span>{new Date().toLocaleTimeString()}</span>
          <ListPaginationControl pagination={pagination} />
        </div>
      </div>
      <div className="queue-stats">
        <Metric label={t("summary.queued")} value={queued} />
        <Metric label={t("filters.running")} value={running} />
        <Metric label={t("filters.blocked")} value={blocked} />
        <Metric label={t("filters.done")} value={completed} />
      </div>
      <div className="capacity-list dashboard-list">
        {snapshots.length ? (
          pagination.items.map((snapshot) => (
            <div className="capacity-row" key={snapshot.executor}>
              <div>
                <strong>{labelize(snapshot.executor)}</strong>
                <span>{t("common.freeSlots", { count: snapshot.available_slots })}</span>
              </div>
              <div className="meter" aria-hidden="true">
                <span style={{ width: `${capacityWidth(snapshot)}%` }} />
              </div>
              <code>{t("common.retrySeconds", { count: snapshot.retry_after_seconds })}</code>
            </div>
          ))
        ) : (
          <div className="empty-state compact">{t("queue.noCapacity")}</div>
        )}
      </div>
      <div className="action-bar compact-actions">
        <button className="primary-button" disabled={dispatchMutation.isPending || !access.has("runs:write")} onClick={() => dispatchMutation.mutate()} type="button">
          {t("queue.dispatch")}
        </button>
        <button className="secondary-button" onClick={() => onSelectView("queue")} type="button">
          {t("queue.open")}
        </button>
      </div>
    </section>
  );
}

function TaskLifecyclePanel({
  activeProject,
  activeRepository,
  approvals,
  latestRun,
  onSelectView,
  runs,
  task
}: {
  activeProject?: Project;
  activeRepository?: Repository;
  approvals: Approval[];
  latestRun?: Run;
  onSelectView: (view: View) => void;
  runs: Run[];
  task?: Task;
}) {
  const { t } = useI18n();
  const access = useAccess();
  const [scopeInput, setScopeInput] = useState("internal/**\napps/web/src/**\ndocs/**");
  const [scopeResult, setScopeResult] = useState<{ status: string; changed_files: string[]; violations: string[] } | undefined>();
  const workflow = useQuery({
    queryKey: ["workflow", task?.id],
    queryFn: () => getWorkflow(task!.id),
    enabled: Boolean(task) && access.has("projects:read"),
    refetchInterval: pollWhileHealthy(2_000)
  });
  const startMutation = useMutation({
    mutationFn: async () => startTask(task!.id),
    onSuccess: (run) => refreshTaskQueries(task, run.id)
  });
  const scopeMutation = useMutation({
    mutationFn: async () => scopeCheck(task!.id, lines(scopeInput)),
    onSuccess: (result) => {
      setScopeResult(result);
      refreshTaskQueries(task);
    }
  });
  const workflowMutation = useMutation({
    mutationFn: (action: string) => runWorkflowAction(task!.id, action),
    onSuccess: () => refreshTaskQueries(task)
  });
  const approvalMutation = useMutation({
    mutationFn: () => requestApproval(task!.id, { approval_type: "pr_prepare", reason: "PR body preparation requires human approval." }),
    onSuccess: () => refreshTaskQueries(task)
  });

  if (!task) {
    return (
      <section className="panel cockpit-panel lifecycle-panel">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">{t("dashboard.delivery")}</p>
            <h3>{t("dashboard.taskLifecycle")}</h3>
          </div>
          <StatusBadge status={activeProject && activeRepository ? "ready" : "setup"} />
        </div>
        <div className="empty-lifecycle">
          <h4>{activeProject && activeRepository ? t("tasks.createTitle") : t("tasks.connectFirst")}</h4>
          <p>{t("tasks.lifecycleHelp")}</p>
          <button className="primary-button" onClick={() => onSelectView("tasks")} type="button">
            {t("tasks.openTasks")}
          </button>
        </div>
      </section>
    );
  }

  const taskApprovals = approvals.filter((approval) => approval.task_id === task.id);
  const pendingApprovals = taskApprovals.filter((approval) => approval.status === "pending").length;
  const blockedReasons = workflow.data?.blocked_reasons ?? [];
  const nextActions = workflow.data?.next_actions ?? [];
  const role = envelopeValue(task.envelope, "role", "feature");
  const executor = envelopeValue(task.envelope, "executor", latestRun?.executor ?? "docker");
  const allowedPaths = envelopeList(task.envelope, "allowed_paths");

  return (
    <section className="panel cockpit-panel lifecycle-panel">
      <div className="lifecycle-header">
        <div>
          <p className="eyebrow">{t("dashboard.selectedTask")}</p>
          <div className="task-title-line">
            <code>{task.task_key}</code>
            <h3>{task.title}</h3>
          </div>
          <div className="task-meta">
            <StatusBadge status={task.status} />
            <span>{roleLabel(role, t)}</span>
            <span>{executor}</span>
            <span>{t("common.recent", { count: runs.length })}</span>
          </div>
        </div>
        <div className="actions">
          <button className="primary-button" disabled={startMutation.isPending || !access.has("runs:write")} onClick={() => startMutation.mutate()} type="button">
            {t("tasks.startTask")}
          </button>
          <button className="secondary-button" disabled={approvalMutation.isPending || !access.has("approvals:write")} onClick={() => approvalMutation.mutate()} type="button">
            {t("tasks.requestApproval")}
          </button>
        </div>
      </div>

      {blockedReasons.length ? (
        <div className="blocker-strip">
          {blockedReasons.map((reason) => (
            <span key={reason}>{reason}</span>
          ))}
        </div>
      ) : (
        <div className="ready-strip">{t("tasks.readyStrip")}</div>
      )}

      <div className="workflow-actions">
        {nextActions.slice(0, 5).map((action) => (
          <button
            key={action}
            className="secondary-button compact"
            disabled={
              workflowMutation.isPending ||
              !access.has("runs:write") ||
              action === "worker_spawn" ||
              action === "policy_validate_task" ||
              action === "approval_status" ||
              action === "completed"
            }
            onClick={() => workflowMutation.mutate(action)}
            type="button"
          >
            {workflowActionLabel(action, t)}
          </button>
        ))}
      </div>

      <div className="lifecycle-steps">
        <LifecycleStep
          detail={t("tasks.allowedPaths") + `: ${allowedPaths.length || 0}`}
          status={allowedPaths.length ? "ready" : "setup"}
          title={t("lifecycle.taskEnvelope")}
          rows={[
            [t("lifecycle.repository"), task.repository_id],
            [t("lifecycle.skill"), envelopeValue(task.envelope, "skill", "company-feature-worker")],
            [t("lifecycle.agentProfile"), envelopeValue(task.envelope, "agent_profile", "feature-worker")]
          ]}
        />
        <LifecycleStep
          detail={workflow.data?.ready_for_pr ? t("lifecycle.readyForPr") : t("lifecycle.policyGates")}
          status={blockedReasons.length ? "blocked" : "validated"}
          title={t("lifecycle.policyValidation")}
          rows={[
            [t("lifecycle.dependencyChange"), policyLabel(task.envelope, "allow_dependency_change", t)],
            [t("lifecycle.infraChange"), policyLabel(task.envelope, "allow_infra_change", t)],
            [t("lifecycle.humanBeforePr"), policyLabel(task.envelope, "require_human_before_pr", t)]
          ]}
        />
        <div className="lifecycle-step scope-step">
          <div className="step-heading">
            <StatusBadge status={scopeResult?.status ?? workflow.data?.latest_scope_check?.result.status ?? "pending"} />
            <div>
              <h4>{t("tasks.scopeCheck")}</h4>
              <span>{scopeResult?.violations.length ? t("tasks.violations", { count: scopeResult.violations.length }) : t("tasks.pathsConstrained")}</span>
            </div>
          </div>
          <div className="scope-form cockpit-scope-form">
            <textarea value={scopeInput} onChange={(event) => setScopeInput(event.target.value)} />
            <button className="secondary-button" disabled={scopeMutation.isPending || !access.has("runs:write")} onClick={() => scopeMutation.mutate()} type="button">
              {t("tasks.check")}
            </button>
          </div>
          {scopeResult ? (
            <div className="scope-result">
              <span>{t("tasks.filesChecked", { count: scopeResult.changed_files.length })}</span>
              <code>{scopeResult.violations.length ? scopeResult.violations.join(", ") : t("tasks.noViolations")}</code>
            </div>
          ) : null}
        </div>
        <LifecycleStep
          detail={latestRun?.executor_node_id || latestRun?.executor || t("lifecycle.awaitingWorker")}
          status={latestRun ? latestRun.status : "pending"}
          title={t("lifecycle.executorIsolation")}
          rows={[
            [t("tasks.executor"), latestRun?.executor ?? executor],
            [t("lifecycle.node"), latestRun?.executor_node_id ?? t("common.notAssigned")],
            [t("lifecycle.worktree"), latestRun?.worktree_path ?? t("common.ephemeral")]
          ]}
        />
        <LifecycleStep
          detail={t("lifecycle.pendingApprovals", { count: pendingApprovals })}
          status={pendingApprovals ? "pending" : taskApprovals.length ? "approved" : "waiting"}
          title={t("lifecycle.auditApproval")}
          rows={[
            [t("lifecycle.approvals"), t("lifecycle.approvalsRequested", { count: taskApprovals.length })],
            [t("lifecycle.readyForPr"), workflow.data?.ready_for_pr ? t("common.yes") : t("common.no")],
            [t("lifecycle.latestRun"), latestRun?.id ?? t("common.noRun")]
          ]}
        />
      </div>
    </section>
  );
}

function LifecycleStep({
  detail,
  rows,
  status,
  title
}: {
  detail: string;
  rows: Array<[string, string]>;
  status: string;
  title: string;
}) {
  return (
    <div className="lifecycle-step">
      <div className="step-heading">
        <StatusBadge status={status} />
        <div>
          <h4>{title}</h4>
          <span>{detail}</span>
        </div>
      </div>
      <dl className="step-rows">
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function EvidenceColumn({
  auditLogs,
  latestRun,
  onSelectView,
  toolCalls
}: {
  auditLogs: AuditLog[];
  latestRun?: Run;
  onSelectView: (view: View) => void;
  toolCalls: ToolCall[];
}) {
  const { t } = useI18n();
  const toolPagination = useListPagination(toolCalls);
  const auditPagination = useListPagination(auditLogs);
  return (
    <div className="cockpit-stack evidence-stack">
      <LiveRunCard run={latestRun} />
      <section className="panel cockpit-panel">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">{t("dashboard.gateway")}</p>
            <h3>{t("dashboard.toolCalls")}</h3>
          </div>
          <div className="panel-heading-actions">
            <ListPaginationControl pagination={toolPagination} />
            <button className="link-button" onClick={() => onSelectView("audit")} type="button">
              {t("dashboard.viewAll")}
            </button>
          </div>
        </div>
        <div className="table-list compact-evidence dashboard-list">
          {toolCalls.length ? (
            toolPagination.items.map((call) => (
              <div className="data-row evidence-row" key={call.id}>
                <div>
                  <strong>{call.tool_name}</strong>
                  <code>{call.caller}</code>
                </div>
                <StatusBadge status={call.status} />
                <code>{call.run_id ?? t("dashboard.gateway")}</code>
              </div>
            ))
          ) : (
            <div className="empty-state compact">{t("dashboard.noToolCalls")}</div>
          )}
        </div>
      </section>
      <ArtifactSummary run={latestRun} />
      <section className="panel cockpit-panel">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">{t("dashboard.evidence")}</p>
            <h3>{t("dashboard.auditTrail")}</h3>
          </div>
          <div className="panel-heading-actions">
            <ListPaginationControl pagination={auditPagination} />
            <button className="link-button" onClick={() => onSelectView("audit")} type="button">
              {t("dashboard.stream")}
            </button>
          </div>
        </div>
        <CompactAuditList entries={auditPagination.items} className="dashboard-list" />
      </section>
    </div>
  );
}

function LiveRunCard({ run }: { run?: Run }) {
  const { t } = useI18n();
  const events = useRunEvents(run?.id, run?.status);
  const eventPagination = useListPagination(events.events, { fromEnd: true });

  return (
    <section className="panel cockpit-panel">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">{t("dashboard.worker")}</p>
          <h3>{run ? t("runs.runTitle", { role: roleLabel(run.role, t) }) : t("dashboard.liveRun")}</h3>
        </div>
        <div className="panel-heading-actions">
          <ListPaginationControl pagination={eventPagination} />
          <StatusBadge status={run?.status ?? "idle"} />
        </div>
      </div>
      {run ? (
        <>
          <dl className="run-facts">
            <div>
              <dt>{t("tasks.executor")}</dt>
              <dd>{run.executor}</dd>
            </div>
            <div>
              <dt>{t("runs.started")}</dt>
              <dd>{formatDate(run.started_at ?? run.created_at, t)}</dd>
            </div>
            <div>
              <dt>{t("lifecycle.node")}</dt>
              <dd>{run.executor_node_id ?? t("common.notAssigned")}</dd>
            </div>
            <div>
              <dt>{t("runs.branch")}</dt>
              <dd>{run.branch ?? t("status.pending")}</dd>
            </div>
          </dl>
          <EventList events={eventPagination.items} streamState={events.streamState} />
        </>
      ) : (
        <div className="empty-state compact">{t("runs.noRuns")}</div>
      )}
    </section>
  );
}

function ArtifactSummary({ run }: { run?: Run }) {
  const { t } = useI18n();
  const artifacts = useQuery({
    queryKey: ["artifacts", run?.id],
    queryFn: () => listArtifacts(run!.id),
    enabled: Boolean(run),
    refetchInterval: pollWhileHealthy(run?.status === "running" ? 2_000 : 5_000)
  });
  const artifactList = artifacts.data ?? [];
  const pagination = useListPagination(artifactList);

  return (
    <section className="panel cockpit-panel">
      <div className="panel-heading">
        <div>
            <p className="eyebrow">{t("dashboard.outputs")}</p>
            <h3>{t("dashboard.artifacts")}</h3>
        </div>
        <div className="panel-heading-actions">
          <span>{t("common.files", { count: artifactList.length })}</span>
          <ListPaginationControl pagination={pagination} />
        </div>
      </div>
      <div className="table-list compact-evidence dashboard-list">
        {artifactList.length ? (
          pagination.items.map((artifact) => (
            <div className="data-row evidence-row" key={artifact.id}>
              <div>
                <strong>{artifact.name}</strong>
                <code>{artifact.kind}</code>
              </div>
              <span>{t("common.bytes", { count: artifact.size_bytes ?? 0 })}</span>
              <code>{artifact.sha256?.slice(0, 12) ?? t("common.noHash")}</code>
            </div>
          ))
        ) : (
          <div className="empty-state compact">{run ? t("artifacts.noArtifactsYet") : t("artifacts.noRunSelected")}</div>
        )}
      </div>
    </section>
  );
}

function ProjectRepoPanel({ activeProjectId }: { activeProjectId?: string }) {
  const { t } = useI18n();
  const access = useAccess();
  const [projectName, setProjectName] = useState("Platform Engineering");
  const [projectSlug, setProjectSlug] = useState("platform-engineering");
  const [repoName, setRepoName] = useState("platform-api");
  const [remoteURL, setRemoteURL] = useState("file:///workspace/platform-api.git");
  const createProjectMutation = useMutation({
    mutationFn: () => createProject({ name: projectName, slug: projectSlug, description: "Managed by multi-codex." }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["projects"] })
  });
  const createRepoMutation = useMutation({
    mutationFn: () =>
      createRepository(activeProjectId!, {
        name: repoName,
        provider: remoteURL.startsWith("file://") ? "local" : "git",
        remote_url: remoteURL,
        default_branch: "main"
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["repositories", activeProjectId] })
  });

  return (
    <div className="panel">
      <div className="panel-heading">
        <h3>{t("dashboard.projectSetup")}</h3>
      </div>
      {!access.has("projects:write") ? <AccessNotice permission="projects:write" /> : null}
      <form
        className="form-grid"
        onSubmit={(event) => {
          event.preventDefault();
          createProjectMutation.mutate();
        }}
      >
        <label>
          {t("topbar.project")}
          <input value={projectName} onChange={(event) => setProjectName(event.target.value)} />
        </label>
        <label>
          {t("common.slug")}
          <input value={projectSlug} onChange={(event) => setProjectSlug(event.target.value)} />
        </label>
        <button className="primary-button" type="submit" disabled={createProjectMutation.isPending || !access.has("projects:write")}>
          {t("dashboard.createProject")}
        </button>
      </form>
      {!access.has("repositories:write") ? <AccessNotice permission="repositories:write" /> : null}
      <form
        className="form-grid"
        onSubmit={(event) => {
          event.preventDefault();
          createRepoMutation.mutate();
        }}
      >
        <label>
          {t("dashboard.addRepository")}
          <input value={repoName} onChange={(event) => setRepoName(event.target.value)} />
        </label>
        <label>
          {t("common.remoteUrl")}
          <input value={remoteURL} onChange={(event) => setRemoteURL(event.target.value)} />
        </label>
        <button className="primary-button" type="submit" disabled={!activeProjectId || createRepoMutation.isPending || !access.has("repositories:write")}>
          {t("topbar.repository")}
        </button>
      </form>
    </div>
  );
}

function TasksView({
  activeProject,
  activeRepository,
  onSelectTask,
  selectedTask,
  tasks
}: {
  activeProject?: Project;
  activeRepository?: Repository;
  onSelectTask: (taskId: string) => void;
  selectedTask?: Task;
  tasks: Task[];
}) {
  const { t } = useI18n();
  const pagination = useListPagination(tasks);
  return (
    <section className="content-grid tasks-layout">
      <div className="panel">
        <div className="panel-heading">
          <h3>{t("nav.tasks")}</h3>
          <div className="panel-heading-actions">
            <span>{t("common.total", { count: tasks.length })}</span>
            <ListPaginationControl pagination={pagination} />
          </div>
        </div>
        <TaskCreateForm activeProject={activeProject} activeRepository={activeRepository} />
        <div className="task-list bounded-list">
          {tasks.length ? (
            pagination.items.map((task) => (
              <TaskRow key={task.id} task={task} active={task.id === selectedTask?.id} onSelect={() => onSelectTask(task.id)} />
            ))
          ) : (
            <div className="empty-state">{t("tasks.noTasks")}</div>
          )}
        </div>
      </div>

      <TaskDetail task={selectedTask} />
    </section>
  );
}

function TaskCreateForm({ activeProject, activeRepository }: { activeProject?: Project; activeRepository?: Repository }) {
  const { t } = useI18n();
  const access = useAccess();
  const [title, setTitle] = useState(t("tasks.defaultTitle"));
  const [allowedPaths, setAllowedPaths] = useState("internal/**\napps/web/src/**\ndocs/**");
  const [objective, setObjective] = useState(t("tasks.defaultObjective"));
  const mutation = useMutation({
    mutationFn: () =>
      createTask(activeProject!, activeRepository!, {
        title,
        allowed_paths: lines(allowedPaths),
        objective
      }),
    onSuccess: (task) => {
      void queryClient.invalidateQueries({ queryKey: ["tasks", activeProject?.id] });
      void queryClient.invalidateQueries({ queryKey: ["audit-logs"] });
      window.location.hash = "tasks";
      return task;
    }
  });

  return (
    <form
      className="form-grid bordered"
      onSubmit={(event: FormEvent) => {
        event.preventDefault();
        mutation.mutate();
      }}
    >
      {!access.has("tasks:write") ? <AccessNotice permission="tasks:write" /> : null}
      <label>
        {t("tasks.title")}
        <input value={title} onChange={(event) => setTitle(event.target.value)} />
      </label>
      <label>
        {t("tasks.objective")}
        <textarea value={objective} onChange={(event) => setObjective(event.target.value)} />
      </label>
      <label>
        {t("tasks.allowedPaths")}
        <textarea value={allowedPaths} onChange={(event) => setAllowedPaths(event.target.value)} />
      </label>
      <button className="primary-button" type="submit" disabled={!activeProject || !activeRepository || mutation.isPending || !access.has("tasks:write")}>
        {t("tasks.create")}
      </button>
    </form>
  );
}

function TaskRow({ task, active, onSelect }: { task: Task; active: boolean; onSelect: () => void }) {
  return (
    <button className={`task-row ${active ? "active" : ""}`} onClick={onSelect}>
      <div>
        <span className="task-key">{task.task_key}</span>
        <h4>{task.title}</h4>
      </div>
      <StatusBadge status={task.status} />
    </button>
  );
}

function TaskDetail({ task }: { task?: Task }) {
  const { t } = useI18n();
  const access = useAccess();
  const [scopeInput, setScopeInput] = useState("internal/policy/scope.go\ndocs/implementation/roadmap.md");
  const [scopeResult, setScopeResult] = useState<{ status: string; changed_files: string[]; violations: string[] } | undefined>();
  const runs = useQuery({
    queryKey: ["runs", task?.id],
    queryFn: () => listRuns(task!.id),
    enabled: Boolean(task) && access.has("runs:read"),
    refetchInterval: pollWhileHealthy(2_000)
  });
  const workflow = useQuery({
    queryKey: ["workflow", task?.id],
    queryFn: () => getWorkflow(task!.id),
    enabled: Boolean(task) && access.has("projects:read"),
    refetchInterval: pollWhileHealthy(2_000)
  });
  const latestRun = runs.data?.[runs.data.length - 1];
  const events = useRunEvents(latestRun?.id, latestRun?.status);
  const runPagination = useListPagination(runs.data ?? []);
  const eventPagination = useListPagination(events.events, { fromEnd: true });
  const startMutation = useMutation({
    mutationFn: async () => startTask(task!.id),
    onSuccess: (run) => refreshTaskQueries(task, run.id)
  });
  const scopeMutation = useMutation({
    mutationFn: async () => scopeCheck(task!.id, lines(scopeInput)),
    onSuccess: (result) => {
      setScopeResult(result);
      refreshTaskQueries(task);
    }
  });
  const workflowMutation = useMutation({
    mutationFn: (action: string) => runWorkflowAction(task!.id, action),
    onSuccess: () => refreshTaskQueries(task)
  });
  const approvalMutation = useMutation({
    mutationFn: () => requestApproval(task!.id, { approval_type: "pr_prepare", reason: "PR body preparation requires human approval." }),
    onSuccess: () => refreshTaskQueries(task)
  });
  const publishApprovalMutation = useMutation({
    mutationFn: () => requestApproval(task!.id, { approval_type: "pr_publish", reason: "PR publish preparation requires explicit human approval." }),
    onSuccess: () => refreshTaskQueries(task)
  });

  if (!task) {
    return (
      <div className="panel detail-panel">
        <div className="empty-state">{t("tasks.selectTask")}</div>
      </div>
    );
  }

  return (
    <div className="panel detail-panel">
      <div className="panel-heading">
        <h3>{task.title}</h3>
        <div className="actions">
          <button className="primary-button" onClick={() => startMutation.mutate()} disabled={startMutation.isPending || !access.has("runs:write")}>
            {t("tasks.start")}
          </button>
          <StatusBadge status={task.status} />
        </div>
      </div>
      <dl className="detail-list">
        <div>
          <dt>{t("tasks.taskKey")}</dt>
          <dd>{task.task_key}</dd>
        </div>
        <div>
          <dt>{t("tasks.repository")}</dt>
          <dd>{task.repository_id}</dd>
        </div>
        <div>
          <dt>{t("tasks.role")}</dt>
          <dd>{roleLabel(String(task.envelope.role ?? "feature"), t)}</dd>
        </div>
        <div>
          <dt>{t("tasks.executor")}</dt>
          <dd>{String(task.envelope.executor ?? "docker")}</dd>
        </div>
      </dl>

      <h4 className="section-title">{t("tasks.workflowGates")}</h4>
      <div className="gate-strip">
        {workflow.data?.blocked_reasons?.length ? (
          workflow.data.blocked_reasons.map((reason) => <span key={reason} className="gate-blocked">{reason}</span>)
        ) : (
          <span className="gate-ok">{t("tasks.noActiveBlockers")}</span>
        )}
        {workflow.data?.next_actions?.map((action) => (
          <button
            key={action}
            className="secondary-button"
            disabled={workflowMutation.isPending || !access.has("runs:write") || action === "worker_spawn" || action === "policy_validate_task" || action === "approval_status" || action === "completed"}
            onClick={() => workflowMutation.mutate(action)}
          >
            {workflowActionLabel(action, t)}
          </button>
        ))}
        <button className="secondary-button" onClick={() => approvalMutation.mutate()} disabled={approvalMutation.isPending || !access.has("approvals:write")}>
          {t("tasks.requestPrApproval")}
        </button>
        <button className="secondary-button" onClick={() => publishApprovalMutation.mutate()} disabled={publishApprovalMutation.isPending || !access.has("approvals:write")}>
          {t("tasks.requestPublishApproval")}
        </button>
      </div>

      <h4 className="section-title">{t("tasks.scopeCheck")}</h4>
      <div className="scope-form">
        <textarea value={scopeInput} onChange={(event) => setScopeInput(event.target.value)} />
        <button className="secondary-button" onClick={() => scopeMutation.mutate()} disabled={scopeMutation.isPending || !access.has("runs:write")}>
          {t("tasks.check")}
        </button>
      </div>
      {scopeResult ? (
        <div className="scope-result">
          <StatusBadge status={scopeResult.status} />
          <span>{t("tasks.filesChecked", { count: scopeResult.changed_files.length })}</span>
          {scopeResult.violations.length ? <code>{scopeResult.violations.join(", ")}</code> : <code>{t("tasks.noViolations")}</code>}
        </div>
      ) : null}

      <div className="section-title-row list-section-title">
        <h4 className="section-title">{t("tasks.runs")}</h4>
        <ListPaginationControl pagination={runPagination} />
      </div>
      <div className="run-list bounded-list compact-bounded-list">
        {!access.has("runs:read") ? <AccessNotice permission="runs:read" /> : null}
        {runs.data?.length ? (
          runPagination.items.map((run) => (
            <div className="run-row" key={run.id}>
              <span>{roleLabel(run.role, t)}</span>
              <StatusBadge status={run.status} />
              <code>{run.executor}</code>
              <code>{run.executor_node_id || t("common.notAssigned")}</code>
            </div>
          ))
        ) : (
          <div className="empty-state compact">{t("runs.noTaskRuns")}</div>
        )}
      </div>

      <div className="section-title-row list-section-title">
        <h4 className="section-title">{t("tasks.latestRunEvents")}</h4>
        <ListPaginationControl pagination={eventPagination} />
      </div>
      <EventList events={eventPagination.items} streamState={events.streamState} />

      <h4 className="section-title">{t("tasks.latestRunArtifacts")}</h4>
      <RunArtifactInspector runId={latestRun?.id} runStatus={latestRun?.status} />

      <h4 className="section-title">{t("tasks.envelope")}</h4>
      <pre className="json-view">{JSON.stringify(task.envelope, null, 2)}</pre>
    </div>
  );
}

function RunsView() {
  const { t } = useI18n();
  const access = useAccess();
  const runs = useQuery({ queryKey: ["runs"], queryFn: listAllRuns, enabled: access.has("runs:read"), refetchInterval: pollWhileHealthy(2_000) });
  const [selectedRunId, setSelectedRunId] = useState<string | undefined>();
  const runList = useMemo(() => runs.data ?? [], [runs.data]);
  const pagination = useListPagination(runList);
  const selectedRun = runList.find((run) => run.id === selectedRunId) ?? runList[0];

  useEffect(() => {
    if (runList.length === 0) {
      setSelectedRunId(undefined);
      return;
    }
    if (!selectedRunId || !runList.some((run) => run.id === selectedRunId)) {
      setSelectedRunId(runList[0].id);
    }
  }, [runList, selectedRunId]);

  return (
    <section className="content-grid runs-layout">
      <div className="panel">
        <div className="panel-heading">
          <h3>{t("nav.runs")}</h3>
          <div className="panel-heading-actions">
            <span>{runs.isLoading ? t("common.loading") : t("common.recent", { count: runList.length })}</span>
            <ListPaginationControl pagination={pagination} />
          </div>
        </div>
        <div className="table-list bounded-list">
          {runList.length ? (
            pagination.items.map((run) => (
              <button
                className={`data-row selectable-row ${run.id === selectedRun?.id ? "is-selected" : ""}`}
                key={run.id}
                onClick={() => setSelectedRunId(run.id)}
                type="button"
              >
                <div>
                  <strong>{roleLabel(run.role, t)}</strong>
                  <code>{run.id}</code>
                </div>
                <StatusBadge status={run.status} />
                <span>{run.executor}</span>
                <code>{run.executor_node_id || run.worktree_path || run.task_id}</code>
              </button>
            ))
          ) : (
            <div className="empty-state">{t("runs.noRuns")}</div>
          )}
        </div>
      </div>
      <RunDetail run={selectedRun} />
    </section>
  );
}

function QueueView() {
  const { t } = useI18n();
  const access = useAccess();
  const queue = useQuery({ queryKey: ["queue"], queryFn: getQueueStatus, enabled: access.has("runs:read"), refetchInterval: pollWhileHealthy(2_000) });
  const dispatchMutation = useMutation({
    mutationFn: dispatchQueue,
    onSuccess: (payload) => {
      void queryClient.invalidateQueries({ queryKey: ["queue"] });
      void queryClient.invalidateQueries({ queryKey: ["runs"] });
      void queryClient.invalidateQueries({ queryKey: ["tasks"] });
      void queryClient.invalidateQueries({ queryKey: ["audit-logs"] });
      void queryClient.invalidateQueries({ queryKey: ["run-events", payload.run.id] });
    }
  });
  const queuedRuns = queue.data?.queued_runs ?? [];
  const backpressure = queue.data?.backpressure ?? {};
  const pagination = useListPagination(queuedRuns);

  return (
    <section className="content-grid queue-layout">
      <div className="panel">
        <div className="panel-heading">
          <h3>{t("queue.workerQueue")}</h3>
          <div className="panel-heading-actions">
            <span>{queue.isLoading ? t("common.loading") : t("common.recent", { count: queuedRuns.length })}</span>
            <ListPaginationControl pagination={pagination} />
            <button className="secondary-button" onClick={() => dispatchMutation.mutate()} disabled={dispatchMutation.isPending || !access.has("runs:write")}>
              {t("queue.dispatch")}
            </button>
          </div>
        </div>
        <div className="table-list bounded-list">
          {queuedRuns.length ? (
            pagination.items.map((run) => (
              <div className="data-row" key={run.id}>
                <div>
                  <strong>{roleLabel(run.role, t)}</strong>
                  <code>{run.id}</code>
                </div>
                <StatusBadge status={run.status} />
                <span>{run.executor}</span>
                <code>{queueValue(run, "queued_reason", "queued")}</code>
                <code>
                  {t("queue.priorityAttempt", {
                    priority: queueValue(run, "queue_priority", "0"),
                    attempt: queueValue(run, "retry_attempt", "1"),
                    max: queueValue(run, "max_attempts", "1")
                  })}
                </code>
              </div>
            ))
          ) : (
            <div className="empty-state">{t("queue.noQueued")}</div>
          )}
        </div>
      </div>

      <div className="panel detail-panel">
        <div className="panel-heading">
          <h3>{t("queue.backpressure")}</h3>
          <StatusBadge status={dispatchMutation.isError ? "blocked" : "ready"} />
        </div>
        {dispatchMutation.isError ? (
          <div className="empty-state compact">{dispatchMutation.error instanceof Error ? dispatchMutation.error.message : t("queue.dispatchFailed")}</div>
        ) : null}
        <BackpressureSection title="Docker" snapshot={backpressure.docker} />
        <BackpressureSection title="SSH" snapshot={backpressure.ssh} />
      </div>
    </section>
  );
}

function BackpressureSection({ title, snapshot }: { title: string; snapshot?: Backpressure }) {
  const { t } = useI18n();
  const pagination = useListPagination(snapshot?.nodes ?? []);
  return (
    <div className="node-section">
      <div className="section-title-row">
        <h4 className="section-title">{title}</h4>
        {snapshot ? (
          <div className="capacity-summary">
            <span>{t("common.free", { count: snapshot.available_slots })}</span>
            <span>{t("common.retrySeconds", { count: snapshot.retry_after_seconds })}</span>
            <ListPaginationControl pagination={pagination} />
          </div>
        ) : null}
      </div>
      {snapshot?.nodes.length ? (
        <div className="node-list bounded-list compact-bounded-list">
          {pagination.items.map((node) => (
            <div className="node-row" key={node.id}>
              <div className="node-primary">
                <strong>{node.name}</strong>
                <code>{node.selection_rank ? `#${node.selection_rank}` : node.ineligible_reason || node.status}</code>
              </div>
              <div className="node-metrics">
                <span>
                  {node.active_runs}/{node.concurrency}
                </span>
                <span>{t("common.free", { count: node.available_slots })}</span>
                <span>{Math.round(node.utilization * 100)}%</span>
              </div>
              <div className="node-meter" aria-hidden="true">
                <span style={{ width: `${Math.min(100, Math.round(node.utilization * 100))}%` }} />
              </div>
              <StatusBadge status={node.eligible ? "ready" : node.ineligible_reason || node.status} />
            </div>
          ))}
        </div>
      ) : (
        <div className="empty-state compact">{t("queue.noNodes")}</div>
      )}
    </div>
  );
}

function queueValue(run: Run, key: string, fallback: string) {
  const value = run.result?.[key];
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (typeof value === "string" && value.length > 0) {
    return value;
  }
  return fallback;
}

function RunDetail({ run }: { run?: Run }) {
  const { t } = useI18n();
  const events = useRunEvents(run?.id, run?.status);
  const eventPagination = useListPagination(events.events, { fromEnd: true });

  if (!run) {
    return (
      <div className="panel detail-panel">
        <div className="empty-state">{t("runs.selectRun")}</div>
      </div>
    );
  }

  return (
    <div className="panel detail-panel">
      <div className="panel-heading">
        <h3>{t("runs.runTitle", { role: roleLabel(run.role, t) })}</h3>
        <StatusBadge status={run.status} />
      </div>
      <dl className="detail-list">
        <div>
          <dt>{t("runs.runId")}</dt>
          <dd>{run.id}</dd>
        </div>
        <div>
          <dt>{t("runs.taskId")}</dt>
          <dd>{run.task_id}</dd>
        </div>
        <div>
          <dt>{t("tasks.executor")}</dt>
          <dd>{run.executor}</dd>
        </div>
        <div>
          <dt>{t("runs.executorNode")}</dt>
          <dd>{run.executor_node_id || t("common.notAssigned")}</dd>
        </div>
        <div>
          <dt>{t("runs.branch")}</dt>
          <dd>{run.branch ?? t("common.notAssigned")}</dd>
        </div>
      </dl>

      <div className="section-title-row list-section-title">
        <h4 className="section-title">{t("runs.events")}</h4>
        <ListPaginationControl pagination={eventPagination} />
      </div>
      <EventList events={eventPagination.items} streamState={events.streamState} />

      <h4 className="section-title">{t("runs.artifacts")}</h4>
      <RunArtifactInspector runId={run.id} runStatus={run.status} />

      <h4 className="section-title">{t("runs.result")}</h4>
      <pre className="json-view">{JSON.stringify(run.result ?? {}, null, 2)}</pre>
    </div>
  );
}

function RunArtifactInspector({ runId, runStatus }: { runId?: string; runStatus?: string }) {
  const { t } = useI18n();
  const access = useAccess();
  const [selectedArtifactId, setSelectedArtifactId] = useState<string | undefined>();
  const artifacts = useQuery({
    queryKey: ["artifacts", runId],
    queryFn: () => listArtifacts(runId!),
    enabled: Boolean(runId) && access.has("runs:read"),
    refetchInterval: pollWhileHealthy(runStatus === "running" ? 2_000 : 5_000)
  });
  const artifactList = useMemo(() => artifacts.data ?? [], [artifacts.data]);
  const pagination = useListPagination(artifactList);
  const selectedArtifact = artifactList.find((artifact) => artifact.id === selectedArtifactId);
  const artifactContent = useQuery({
    queryKey: ["artifact-content", selectedArtifactId],
    queryFn: () => getArtifactContent(selectedArtifactId!),
    enabled: Boolean(selectedArtifactId) && access.has("runs:read"),
    refetchInterval:
      selectedArtifact?.kind === "worker_log" && runStatus === "running" ? pollWhileHealthy(2_000) : false
  });

  useEffect(() => {
    if (!runId || artifactList.length === 0) {
      setSelectedArtifactId(undefined);
      return;
    }
    if (selectedArtifactId && artifactList.some((artifact) => artifact.id === selectedArtifactId)) {
      return;
    }
    const preferred =
      artifactList.find((artifact) => ["worker_log", "diff", "result", "remote_result", "pr_body"].includes(artifact.kind)) ?? artifactList[0];
    setSelectedArtifactId(preferred.id);
  }, [artifactList, runId, selectedArtifactId]);

  return (
    <div className="artifact-shell">
      <div className="inline-list-toolbar">
        <span>{t("common.files", { count: artifactList.length })}</span>
        <ListPaginationControl pagination={pagination} />
      </div>
      <div className="table-list compact-table artifact-list bounded-list compact-bounded-list">
        {artifactList.length ? (
          pagination.items.map((artifact) => (
            <button
              className={`data-row artifact-row ${artifact.id === selectedArtifactId ? "is-selected" : ""}`}
              key={artifact.id}
              onClick={() => setSelectedArtifactId(artifact.id)}
              type="button"
            >
              <div>
                <strong>{artifact.name}</strong>
                <code>{artifact.kind}</code>
              </div>
              <span>{t("common.bytes", { count: artifact.size_bytes ?? 0 })}</span>
              <code>{artifact.sha256?.slice(0, 16) ?? t("common.noHash")}</code>
              <code>{artifact.path}</code>
            </button>
          ))
        ) : (
          <div className="empty-state compact">{runId ? t("artifacts.noArtifactsYet") : t("artifacts.noRunSelected")}</div>
        )}
      </div>
      {selectedArtifact ? (
        <div className="artifact-content-panel">
          <div className="artifact-content-heading">
            <strong>{selectedArtifact.name}</strong>
            <code>{artifactContent.data?.content_type ?? selectedArtifact.kind}</code>
            {artifactContent.data?.truncated ? <span>{t("artifacts.truncated", { bytes: artifactContent.data.limit_bytes })}</span> : null}
          </div>
          <pre className={`artifact-view ${selectedArtifact.kind === "diff" ? "diff-view" : ""}`}>
            {artifactContent.isLoading
              ? t("artifacts.loading")
              : artifactContent.isError
                ? artifactContent.error instanceof Error
                  ? artifactContent.error.message
                  : t("artifacts.failed")
                : artifactContent.data?.content ?? ""}
          </pre>
        </div>
      ) : null}
    </div>
  );
}

function SkillsView({ projectId }: { projectId?: string }) {
  const { t } = useI18n();
  const access = useAccess();
  const skills = useQuery({ queryKey: ["skills"], queryFn: listSkills, enabled: access.has("projects:read") });
  const skillList = useMemo(() => skills.data ?? [], [skills.data]);
  const [selectedSkillId, setSelectedSkillId] = useState<string | undefined>();
  const skillPagination = useListPagination(skillList);
  const selectedSkill = skillList.find((skill) => skill.id === selectedSkillId) ?? skillList[0];
  const versions = useQuery({
    queryKey: ["skill-versions", selectedSkill?.id],
    queryFn: () => listSkillVersions(selectedSkill!.id),
    enabled: Boolean(selectedSkill) && access.has("projects:read")
  });
  const profiles = useQuery({
    queryKey: ["agent-profiles", projectId],
    queryFn: () => listAgentProfiles(projectId!),
    enabled: Boolean(projectId) && access.has("projects:read")
  });
  const profilePagination = useListPagination(profiles.data ?? []);
  const [skillName, setSkillName] = useState("company-docs-worker");
  const [skillRole, setSkillRole] = useState("docs");
  const [profileName, setProfileName] = useState("docs-worker");
  const [profileRole, setProfileRole] = useState("docs");
  const [profileNetworkEnabled, setProfileNetworkEnabled] = useState(false);
  const [profileSecretEnv, setProfileSecretEnv] = useState("");
  const requestedSecretEnv = parseListInput(profileSecretEnv);
  const skillMutation = useMutation({
    mutationFn: () =>
      createSkill({
        name: skillName,
        role: skillRole,
        description: "Registered from the Web Console.",
        version: "local",
        path: `skills/${skillName}/SKILL.md`
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["skills"] });
      void queryClient.invalidateQueries({ queryKey: ["skill-versions"] });
    }
  });
  const profileMutation = useMutation({
    mutationFn: () =>
      createAgentProfile(projectId!, {
        name: profileName,
        role: profileRole,
        model: "gpt-5",
        sandbox_mode: "workspace-write",
        approval_policy: "never",
        executor: "docker",
        image: "multi-codex/codex-worker:go1.25-node-vite8",
        network_enabled: profileNetworkEnabled,
        config: requestedSecretEnv.length ? { worker_secret_env: requestedSecretEnv } : {}
      } as Omit<AgentProfile, "id" | "project_id" | "created_at">),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["agent-profiles", projectId] })
  });

  useEffect(() => {
    if (skillList.length === 0) {
      setSelectedSkillId(undefined);
      return;
    }
    if (!selectedSkillId || !skillList.some((skill) => skill.id === selectedSkillId)) {
      setSelectedSkillId(skillList[0].id);
    }
  }, [skillList, selectedSkillId]);

  return (
    <section className="content-grid skills-layout">
      <div className="panel">
        <div className="panel-heading">
          <h3>{t("skills.skills")}</h3>
          <div className="panel-heading-actions">
            <span>{t("common.registered", { count: skills.data?.length ?? 0 })}</span>
            <ListPaginationControl pagination={skillPagination} />
          </div>
        </div>
        {!access.has("skills:write") ? <AccessNotice permission="skills:write" /> : null}
        <form className="form-grid bordered" onSubmit={(event) => submit(event, () => skillMutation.mutate())}>
          <label>
            {t("skills.name")}
            <input value={skillName} onChange={(event) => setSkillName(event.target.value)} />
          </label>
          <label>
            {t("skills.role")}
            <input value={skillRole} onChange={(event) => setSkillRole(event.target.value)} />
          </label>
          <button className="primary-button" type="submit" disabled={skillMutation.isPending || !access.has("skills:write")}>
            {t("skills.registerSkill")}
          </button>
        </form>
        <div className="table-list bounded-list">
          {skillPagination.items.map((skill) => (
            <button className={`data-row selectable-row ${skill.id === selectedSkill?.id ? "is-selected" : ""}`} key={skill.id} onClick={() => setSelectedSkillId(skill.id)}>
              <div>
                <strong>{skill.name}</strong>
                <code>{skill.version ?? "local"}</code>
              </div>
              <StatusBadge status={skill.enabled ? "enabled" : "disabled"} />
              <span>{skill.role}</span>
              <code>{skill.path ?? t("common.noPath")}</code>
            </button>
          ))}
        </div>
        <SkillVersionList versions={versions.data ?? []} />
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>{t("skills.agentProfiles")}</h3>
          <div className="panel-heading-actions">
            <span>{t("common.profiles", { count: profiles.data?.length ?? 0 })}</span>
            <ListPaginationControl pagination={profilePagination} />
          </div>
        </div>
        {!access.has("projects:write") ? <AccessNotice permission="projects:write" /> : null}
        <form className="form-grid bordered" onSubmit={(event) => submit(event, () => profileMutation.mutate())}>
          <label>
            {t("skills.name")}
            <input value={profileName} onChange={(event) => setProfileName(event.target.value)} />
          </label>
          <label>
            {t("skills.role")}
            <input value={profileRole} onChange={(event) => setProfileRole(event.target.value)} />
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={profileNetworkEnabled}
              onChange={(event) => setProfileNetworkEnabled(event.target.checked)}
            />
            {t("skills.network")}
          </label>
          <label>
            {t("skills.secretEnvRefs")}
            <input value={profileSecretEnv} onChange={(event) => setProfileSecretEnv(event.target.value)} placeholder="OPENAI_API_KEY, CODEX_AUTH_TOKEN" />
          </label>
          <button className="primary-button" type="submit" disabled={!projectId || profileMutation.isPending || !access.has("projects:write")}>
            {t("skills.createProfile")}
          </button>
        </form>
        <div className="table-list bounded-list">
          {profilePagination.items.map((profile) => (
            <div className="data-row" key={profile.id}>
              <div>
                <strong>{profile.name}</strong>
                <code>{profile.model}</code>
              </div>
              <StatusBadge status={profile.executor} />
              <span>{profile.role}</span>
              <code>{profile.network_enabled ? t("skills.networkOn") : t("skills.networkOff")}</code>
              <code>{profileSecretEnvLabel(profile, t)}</code>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function SkillVersionList({ versions }: { versions: SkillVersion[] }) {
  const { t } = useI18n();
  const pagination = useListPagination(versions);
  return (
    <div className="version-shell">
      <div className="inline-list-toolbar">
        <span>{t("common.total", { count: versions.length })}</span>
        <ListPaginationControl pagination={pagination} />
      </div>
      <div className="version-list bounded-list compact-bounded-list">
        {versions.length ? (
          pagination.items.map((version) => (
            <div className="version-row" key={version.id}>
              <strong>{version.version}</strong>
              <code>{version.content_hash}</code>
              <code>{version.path}</code>
            </div>
          ))
        ) : (
          <div className="empty-state compact">{t("skills.noVersions")}</div>
        )}
      </div>
    </div>
  );
}

function parseListInput(value: string): string[] {
  const seen = new Set<string>();
  const values: string[] = [];
  for (const item of value.split(/[,\s;]+/)) {
    const trimmed = item.trim();
    if (!trimmed || seen.has(trimmed)) {
      continue;
    }
    seen.add(trimmed);
    values.push(trimmed);
  }
  return values;
}

function profileSecretEnvLabel(profile: AgentProfile, t: (key: string) => string): string {
  const value = profile.config["worker_secret_env"] ?? profile.config["secret_env"];
  if (Array.isArray(value)) {
    const names = value.filter((item): item is string => typeof item === "string");
    return names.length ? names.join(", ") : t("common.noSecretRefs");
  }
  if (typeof value === "string" && value.trim()) {
    return value;
  }
  return t("common.noSecretRefs");
}

function AdminView({
  activeProject,
  onSelectProject,
  projects,
  users
}: {
  activeProject?: Project;
  onSelectProject: (projectId: string) => void;
  projects: Project[];
  users?: UserDirectory;
}) {
  const { t } = useI18n();
  const access = useAccess();
  const [email, setEmail] = useState("teammate@example.com");
  const [displayName, setDisplayName] = useState("Teammate");
  const [orgRole, setOrgRole] = useState("viewer");
  const [password, setPassword] = useState("ChangeMe123");
  const [selectedUserId, setSelectedUserId] = useState("");
  const [projectRoleDraft, setProjectRoleDraft] = useState("developer");
  const directoryUsers = users?.users ?? [];
  const memberships = users?.memberships ?? [];
  const projectMemberships = users?.project_memberships ?? [];
  const activeProjectMembers = useQuery({
    queryKey: ["project-members", activeProject?.id],
    queryFn: () => listProjectMembers(activeProject!.id),
    enabled: Boolean(activeProject) && access.has("projects:read")
  });
  const projectMembers = activeProjectMembers.data ?? [];
  const selectedUser = directoryUsers.find((user) => user.id === selectedUserId) ?? directoryUsers[0];
  const selectedUserProjectMemberships = selectedUser ? projectMembershipsForUser(projectMemberships, selectedUser.id) : [];
  const userPagination = useListPagination(directoryUsers);
  const memberPagination = useListPagination(projectMembers);
  const projectPagination = useListPagination(projects);
  const accessPagination = useListPagination(selectedUserProjectMemberships);

  const createUserMutation = useMutation({
    mutationFn: () => createUser({ email, display_name: displayName, role: orgRole, password: password.trim() || undefined }),
    onSuccess: (created) => {
      setSelectedUserId(created.user.id);
      setPassword("");
      void queryClient.invalidateQueries({ queryKey: ["users"] });
      void queryClient.invalidateQueries({ queryKey: ["auth"] });
      void queryClient.invalidateQueries({ queryKey: ["audit-logs"] });
    }
  });
  const projectMemberMutation = useMutation({
    mutationFn: () => upsertProjectMember(activeProject!.id, { user_id: selectedUser!.id, role: projectRoleDraft }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["project-members", activeProject?.id] });
      void queryClient.invalidateQueries({ queryKey: ["users"] });
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      void queryClient.invalidateQueries({ queryKey: ["auth"] });
      void queryClient.invalidateQueries({ queryKey: ["audit-logs"] });
    }
  });

  useEffect(() => {
    if (!selectedUserId && directoryUsers[0]) {
      setSelectedUserId(directoryUsers[0].id);
    }
  }, [directoryUsers, selectedUserId]);

  return (
    <section className="admin-grid">
      <div className="admin-main">
        <div className="panel">
          <div className="panel-heading">
            <div>
              <p className="eyebrow">{t("admin.identity")}</p>
              <h3>{t("admin.users")}</h3>
            </div>
            <div className="panel-heading-actions">
              <span>{t("common.total", { count: directoryUsers.length })}</span>
              <ListPaginationControl pagination={userPagination} />
            </div>
          </div>
          {!access.has("users:write") ? <AccessNotice permission="users:write" /> : null}
          <form className="form-grid bordered admin-user-form" onSubmit={(event) => submit(event, () => createUserMutation.mutate())}>
            <label>
              {t("admin.email")}
              <input value={email} onChange={(event) => setEmail(event.target.value)} />
            </label>
            <label>
              {t("admin.displayName")}
              <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} />
            </label>
            <label>
              {t("admin.orgRole")}
              <select className="title-select" value={orgRole} onChange={(event) => setOrgRole(event.target.value)}>
                {orgRoles.map((role) => (
                  <option key={role} value={role}>
                    {roleLabel(role, t)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              {t("admin.password")}
              <input
                autoComplete="new-password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder={t("admin.passwordPlaceholder")}
              />
            </label>
            <button className="primary-button" type="submit" disabled={createUserMutation.isPending || !access.has("users:write")}>
              {t("admin.saveUser")}
            </button>
          </form>
          <div className="table-list admin-user-list bounded-list">
            {directoryUsers.length ? (
              userPagination.items.map((user) => {
                const role = orgRoleForUser(memberships, user.id);
                const projectCount = projectMemberships.filter((membership) => membership.user_id === user.id).length;
                return (
                  <button
                    className={`data-row selectable-row admin-user-row ${user.id === selectedUser?.id ? "is-selected" : ""}`}
                    key={user.id}
                    onClick={() => setSelectedUserId(user.id)}
                    type="button"
                  >
                    <div>
                      <strong>{user.display_name || user.email}</strong>
                      <code>{user.email}</code>
                    </div>
                    <StatusBadge status={role || "viewer"} />
                    <span>{t("admin.projectCount", { count: projectCount })}</span>
                    <code>{user.external_provider || t("admin.localUser")}</code>
                  </button>
                );
              })
            ) : (
              <div className="empty-state">{t("admin.noUsers")}</div>
            )}
          </div>
        </div>

        <div className="panel">
          <div className="panel-heading">
            <div>
              <p className="eyebrow">{t("admin.activeProject")}</p>
              <h3>{activeProject?.name ?? t("admin.noProject")}</h3>
            </div>
            <div className="panel-heading-actions">
              <span>{t("common.total", { count: projectMembers.length })}</span>
              <ListPaginationControl pagination={memberPagination} />
            </div>
          </div>
          {!access.has("projects:write") ? <AccessNotice permission="projects:write" /> : null}
          <form className="form-grid bordered admin-member-form" onSubmit={(event) => submit(event, () => projectMemberMutation.mutate())}>
            <label>
              {t("admin.user")}
              <select className="title-select" value={selectedUser?.id ?? ""} onChange={(event) => setSelectedUserId(event.target.value)}>
                {directoryUsers.map((user) => (
                  <option key={user.id} value={user.id}>
                    {user.display_name || user.email}
                  </option>
                ))}
              </select>
            </label>
            <label>
              {t("admin.projectRole")}
              <select className="title-select" value={projectRoleDraft} onChange={(event) => setProjectRoleDraft(event.target.value)}>
                {projectRoles.map((role) => (
                  <option key={role} value={role}>
                    {roleLabel(role, t)}
                  </option>
                ))}
              </select>
            </label>
            <button
              className="primary-button"
              type="submit"
              disabled={!activeProject || !selectedUser || projectMemberMutation.isPending || !access.has("projects:write")}
            >
              {t("admin.addMember")}
            </button>
          </form>
          <div className="table-list admin-member-list bounded-list">
            {projectMembers.length ? (
              memberPagination.items.map((membership) => (
                <div className="data-row admin-member-row" key={`${membership.project_id}:${membership.user_id}`}>
                  <div>
                    <strong>{membership.user_name || membership.user_email || membership.user_id}</strong>
                    <code>{membership.user_email || membership.user_id}</code>
                  </div>
                  <StatusBadge status={membership.role} />
                  <span>{membership.project_name || activeProject?.name || membership.project_id}</span>
                  <code>{new Date(membership.created_at).toLocaleDateString()}</code>
                </div>
              ))
            ) : (
              <div className="empty-state">{activeProject ? t("admin.noMembers") : t("admin.noProjectMembers")}</div>
            )}
          </div>
        </div>
      </div>

      <aside className="admin-side">
        <div className="panel">
          <div className="panel-heading">
            <div>
              <p className="eyebrow">{t("admin.scope")}</p>
              <h3>{t("admin.projects")}</h3>
            </div>
            <div className="panel-heading-actions">
              <span>{t("common.total", { count: projects.length })}</span>
              <ListPaginationControl pagination={projectPagination} />
            </div>
          </div>
          <div className="project-access-list bounded-list compact-bounded-list">
            {projects.length ? (
              projectPagination.items.map((project) => {
                const count = projectMemberships.filter((membership) => membership.project_id === project.id).length;
                return (
                  <button
                    className={`project-access-row ${project.id === activeProject?.id ? "is-selected" : ""}`}
                    key={project.id}
                    onClick={() => onSelectProject(project.id)}
                    type="button"
                  >
                    <span>
                      <strong>{project.name}</strong>
                      <code>{project.slug}</code>
                    </span>
                    <em>{t("admin.projectMembers", { count })}</em>
                  </button>
                );
              })
            ) : (
              <div className="empty-state compact">{t("admin.noProjects")}</div>
            )}
          </div>
        </div>

        <div className="panel">
          <div className="panel-heading">
            <div>
              <p className="eyebrow">{t("admin.selectedUser")}</p>
              <h3>{selectedUser?.display_name || selectedUser?.email || t("admin.noUser")}</h3>
            </div>
            <div className="panel-heading-actions">
              <span>{t("common.total", { count: selectedUserProjectMemberships.length })}</span>
              <ListPaginationControl pagination={accessPagination} />
            </div>
          </div>
          {selectedUser ? (
            <div className="user-access-card">
              <dl className="detail-list">
                <div>
                  <dt>{t("admin.orgRole")}</dt>
                  <dd>{roleLabel(orgRoleForUser(memberships, selectedUser.id), t)}</dd>
                </div>
                <div>
                  <dt>{t("admin.createdAt")}</dt>
                  <dd>{formatDate(selectedUser.created_at, t)}</dd>
                </div>
              </dl>
              <div className="permission-stack user-project-stack bounded-list compact-bounded-list">
                {selectedUserProjectMemberships.length ? (
                  accessPagination.items.map((membership) => (
                    <code key={`${membership.project_id}:${membership.user_id}`}>
                      {membership.project_name || membership.project_slug || membership.project_id}: {roleLabel(membership.role, t)}
                    </code>
                  ))
                ) : (
                  <code>{t("admin.noProjectAccess")}</code>
                )}
              </div>
            </div>
          ) : (
            <div className="empty-state">{t("admin.noUser")}</div>
          )}
        </div>
      </aside>
    </section>
  );
}

const orgRoles = ["owner", "admin", "tech_lead", "operator", "reviewer", "auditor", "viewer"];
const projectRoles = ["owner", "project_admin", "maintainer", "developer", "reviewer", "auditor", "viewer"];

function orgRoleForUser(memberships: UserDirectory["memberships"], userId: string) {
  return memberships.find((membership) => membership.user_id === userId)?.role ?? "viewer";
}

function projectMembershipsForUser(memberships: ProjectMembership[], userId: string) {
  return memberships
    .filter((membership) => membership.user_id === userId)
    .sort((first, second) => (first.project_name || first.project_id).localeCompare(second.project_name || second.project_id));
}

function roleLabel(role: string, t?: (key: string) => string) {
  const normalized = role.toLowerCase().replaceAll("_", "-");
  if (t) {
    const translated = t(`status.${normalized}`);
    if (translated !== `status.${normalized}`) {
      return translated;
    }
  }
  return labelize(role);
}

function ApprovalsView() {
  const { t } = useI18n();
  const access = useAccess();
  const approvals = useQuery({ queryKey: ["approvals"], queryFn: listApprovals, enabled: access.has("projects:read"), refetchInterval: pollWhileHealthy(3_000) });
  const approvalList = approvals.data ?? [];
  const pagination = useListPagination(approvalList);
  const mutation = useMutation({
    mutationFn: ({ approval, status }: { approval: Approval; status: "approved" | "rejected" }) =>
      decideApproval(approval.id, status, status === "approved" ? "Approved in Web Console." : "Rejected in Web Console."),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["approvals"] });
      void queryClient.invalidateQueries({ queryKey: ["audit-logs"] });
    }
  });

  return (
    <section className="panel">
      <div className="panel-heading">
        <h3>{t("approvals.center")}</h3>
        <div className="panel-heading-actions">
          <span>{t("common.requests", { count: approvalList.length })}</span>
          <ListPaginationControl pagination={pagination} />
        </div>
      </div>
      <div className="table-list bounded-list">
        {approvals.data?.length ? (
          pagination.items.map((approval) => (
            <div className="data-row" key={approval.id}>
              <div>
                <strong>{approval.approval_type}</strong>
                <code>{approval.task_id}</code>
              </div>
              <StatusBadge status={approval.status} />
              <span>{approval.reason || t("approvals.noReason")}</span>
              <div className="actions">
                <button className="secondary-button" disabled={approval.status !== "pending" || !access.has("approvals:write")} onClick={() => mutation.mutate({ approval, status: "approved" })}>
                  {t("approvals.approve")}
                </button>
                <button className="secondary-button danger" disabled={approval.status !== "pending" || !access.has("approvals:write")} onClick={() => mutation.mutate({ approval, status: "rejected" })}>
                  {t("approvals.reject")}
                </button>
              </div>
            </div>
          ))
        ) : (
          <div className="empty-state">{t("approvals.empty")}</div>
        )}
      </div>
    </section>
  );
}

function NodesView() {
  const { t } = useI18n();
  const access = useAccess();
  const nodes = useQuery({ queryKey: ["executor-nodes"], queryFn: listExecutorNodes, enabled: access.has("nodes:read") });
  const nodeList = nodes.data ?? [];
  const pagination = useListPagination(nodeList);
  const [name, setName] = useState("ssh-worker-1");
  const [address, setAddress] = useState("codex-worker@example.invalid:22");
  const [agentDURL, setAgentDURL] = useState("http://worker-agentd-dev:7070");
  const [fingerprint, setFingerprint] = useState("SHA256:multi-codex-agentd-dev");
  const [forcedCommand, setForcedCommand] = useState("multi-codex-worker-agentd --forced-command");
  const mutation = useMutation({
    mutationFn: () =>
      registerExecutorNode({
        kind: "ssh",
        name,
        address,
        agentd_url: agentDURL,
        host_key_fingerprint: fingerprint,
        forced_command: forcedCommand,
        status: "registered"
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["executor-nodes"] })
  });
  const verifyMutation = useMutation({
    mutationFn: ({ nodeId, observed }: { nodeId: string; observed: string }) => verifyExecutorNodeHostKey(nodeId, observed),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["executor-nodes"] })
  });

  return (
    <section className="panel">
      <div className="panel-heading">
        <h3>{t("nodes.executorNodes")}</h3>
        <div className="panel-heading-actions">
          <span>{t("common.available", { count: nodeList.length })}</span>
          <ListPaginationControl pagination={pagination} />
        </div>
      </div>
      {!access.has("nodes:write") ? <AccessNotice permission="nodes:write" /> : null}
      <form className="form-grid bordered inline-form node-form" onSubmit={(event) => submit(event, () => mutation.mutate())}>
        <label>
          {t("nodes.sshNode")}
          <input value={name} onChange={(event) => setName(event.target.value)} />
        </label>
        <label>
          {t("nodes.address")}
          <input value={address} onChange={(event) => setAddress(event.target.value)} />
        </label>
        <label>
          {t("nodes.agentdUrl")}
          <input value={agentDURL} onChange={(event) => setAgentDURL(event.target.value)} />
        </label>
        <label>
          {t("nodes.hostKeyFingerprint")}
          <input value={fingerprint} onChange={(event) => setFingerprint(event.target.value)} />
        </label>
        <label>
          {t("nodes.forcedCommand")}
          <input value={forcedCommand} onChange={(event) => setForcedCommand(event.target.value)} />
        </label>
        <button className="primary-button" type="submit" disabled={mutation.isPending || !access.has("nodes:write")}>
          {t("nodes.register")}
        </button>
      </form>
      <div className="table-list bounded-list">
        {pagination.items.map((node) => (
          <div className="data-row" key={node.id}>
            <div>
              <strong>{node.name}</strong>
              <code>{node.id}</code>
            </div>
            <StatusBadge status={node.status} />
            <span>{node.kind}</span>
            <div className="actions">
              <StatusBadge status={node.host_key_verified ? "verified" : "unverified"} />
              <button
                className="secondary-button"
                disabled={!node.host_key_fingerprint || verifyMutation.isPending || !access.has("nodes:write")}
                onClick={() => verifyMutation.mutate({ nodeId: node.id, observed: node.host_key_fingerprint ?? "" })}
              >
                {t("nodes.verify")}
              </button>
            </div>
            <code>{node.agentd_url ?? node.address ?? t("common.noEndpoint")}</code>
          </div>
        ))}
      </div>
    </section>
  );
}

function OrganizationsView({ organizations }: { organizations: Organization[] }) {
  const { t } = useI18n();
  const access = useAccess();
  const pagination = useListPagination(organizations);
  const [name, setName] = useState("Engineering");
  const [slug, setSlug] = useState("engineering");
  const mutation = useMutation({
    mutationFn: () => createOrganization({ name, slug }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["organizations"] });
      void queryClient.invalidateQueries({ queryKey: ["audit-logs"] });
    }
  });

  return (
    <section className="panel">
      <div className="panel-heading">
        <h3>{t("organizations.organizations")}</h3>
        <div className="panel-heading-actions">
          <span>{t("common.provisioned", { count: organizations.length })}</span>
          <ListPaginationControl pagination={pagination} />
        </div>
      </div>
      {!access.has("organizations:write") ? <AccessNotice permission="organizations:write" /> : null}
      <form className="form-grid bordered inline-form" onSubmit={(event) => submit(event, () => mutation.mutate())}>
        <label>
          {t("organizations.name")}
          <input value={name} onChange={(event) => setName(event.target.value)} />
        </label>
        <label>
          {t("organizations.slug")}
          <input value={slug} onChange={(event) => setSlug(event.target.value)} />
        </label>
        <button className="primary-button" type="submit" disabled={mutation.isPending || !access.has("organizations:write")}>
          {t("organizations.provision")}
        </button>
      </form>
      <div className="table-list bounded-list">
        {organizations.length ? (
          pagination.items.map((org) => (
            <div className="data-row" key={org.id}>
              <div>
                <strong>{org.name}</strong>
                <code>{org.id}</code>
              </div>
              <StatusBadge status="active" />
              <span>{org.slug}</span>
              <code>{new Date(org.created_at).toLocaleString()}</code>
            </div>
          ))
        ) : (
          <div className="empty-state">{t("organizations.empty")}</div>
        )}
      </div>
    </section>
  );
}

function AuditView() {
  const { t } = useI18n();
  const access = useAccess();
  const auditLogs = useQuery({ queryKey: ["audit-logs"], queryFn: listAuditLogs, enabled: access.has("audit:read"), refetchInterval: pollWhileHealthy(5_000) });
  const toolCalls = useQuery({ queryKey: ["tool-calls"], queryFn: listToolCalls, enabled: access.has("audit:read"), refetchInterval: pollWhileHealthy(5_000) });
  const auditList = auditLogs.data ?? [];
  const toolCallList = toolCalls.data ?? [];
  const auditPagination = useListPagination(auditList);
  const toolPagination = useListPagination(toolCallList);

  return (
    <section className="content-grid audit-layout">
      <div className="panel">
        <div className="panel-heading">
          <h3>{t("audit.logs")}</h3>
          <div className="panel-heading-actions">
            <span>{t("common.recent", { count: auditList.length })}</span>
            <ListPaginationControl pagination={auditPagination} />
          </div>
        </div>
        <CompactAuditList entries={auditPagination.items} className="bounded-list" />
      </div>
      <div className="panel">
        <div className="panel-heading">
          <h3>{t("dashboard.toolCalls")}</h3>
          <div className="panel-heading-actions">
            <span>{t("common.recent", { count: toolCallList.length })}</span>
            <ListPaginationControl pagination={toolPagination} />
          </div>
        </div>
        <div className="table-list bounded-list">
          {toolPagination.items.map((call) => (
            <div className="data-row" key={call.id}>
              <div>
                <strong>{call.tool_name}</strong>
                <code>{call.caller}</code>
              </div>
              <StatusBadge status={call.status} />
              <span>{call.run_id || t("common.noRun")}</span>
              <code>{new Date(call.created_at).toLocaleString()}</code>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function CompactAuditList({
  className,
  entries
}: {
  className?: string;
  entries: Array<{ id: string; action: string; resource_type: string; resource_id: string; entry_hash?: string; prev_hash?: string }>;
}) {
  const { t } = useI18n();
  return (
    <div className={`audit-list ${className ?? ""}`}>
      {entries.length ? (
        entries.map((entry) => (
          <div className="audit-row" key={entry.id}>
            <span>{entry.action}</span>
            <div>
              <code>
                {entry.resource_type}:{entry.resource_id}
              </code>
              {entry.entry_hash ? <code>hash:{entry.entry_hash.slice(0, 16)}</code> : null}
            </div>
          </div>
        ))
      ) : (
        <div className="empty-state compact">{t("audit.empty")}</div>
      )}
    </div>
  );
}

type EventStreamState = "idle" | "connecting" | "live" | "fallback";

function useRunEvents(runId?: string, runStatus?: string) {
  const [streamEvents, setStreamEvents] = useState<RunEvent[]>([]);
  const [streamState, setStreamState] = useState<EventStreamState>("idle");
  const fallback = useQuery({
    queryKey: ["run-events", runId],
    queryFn: () => listRunEvents(runId!),
    enabled: Boolean(runId),
    refetchInterval: streamState === "live" ? false : pollWhileHealthy(runStatus === "running" ? 2_000 : 5_000)
  });

  useEffect(() => {
    setStreamEvents([]);
    if (!runId || typeof EventSource === "undefined") {
      setStreamState(runId ? "fallback" : "idle");
      return;
    }
    setStreamState("connecting");
    const source = new EventSource(runEventStreamURL(runId), { withCredentials: true });
    source.onopen = () => setStreamState("live");
    source.onmessage = (message) => {
      const event = parseRunEventPayload(message.data);
      if (!event) {
        return;
      }
      setStreamEvents((current) => mergeRunEvents(current, [event]));
    };
    source.onerror = () => {
      source.close();
      setStreamState("fallback");
    };
    return () => source.close();
  }, [runId]);

  return {
    events: mergeRunEvents(fallback.data ?? [], streamEvents),
    streamState
  };
}

function mergeRunEvents(first: RunEvent[], second: RunEvent[]) {
  const byID = new Map<number, RunEvent>();
  for (const event of first) {
    byID.set(event.id, event);
  }
  for (const event of second) {
    byID.set(event.id, event);
  }
  return Array.from(byID.values()).sort((a, b) => a.seq - b.seq);
}

function EventList({
  events,
  streamState
}: {
  events: RunEvent[];
  streamState?: EventStreamState;
}) {
  const { t } = useI18n();
  const stateLabel = streamState === "live" ? t("runs.live") : streamState === "connecting" ? t("runs.connecting") : streamState === "fallback" ? t("runs.polling") : "";
  return (
    <div className="event-list">
      {stateLabel ? <div className={`stream-state ${streamState}`}>{stateLabel}</div> : null}
      {events.length ? (
        events.map((event) => (
          <div className="event-row" key={event.id}>
            <span>{event.seq}</span>
            <strong>{event.event_type}</strong>
            <p>{event.message}</p>
          </div>
        ))
      ) : (
        <div className="empty-state compact">{t("runs.noEvents")}</div>
      )}
    </div>
  );
}

function labelize(value: string) {
  return value
    .replaceAll("_", " ")
    .replaceAll("-", " ")
    .replace(/\b\w/g, (match) => match.toUpperCase());
}

function workflowActionLabel(action: string, t: (key: string) => string) {
  const key = `workflowAction.${action}`;
  const translated = t(key);
  return translated === key ? labelize(action) : translated;
}

function pollWhileHealthy(interval: number) {
  return (query: { state: { error: unknown } }) => (query.state.error ? false : interval);
}

function formatDate(value: string | undefined, t: (key: string) => string) {
  if (!value) {
    return t("common.notRecorded");
  }
  return new Date(value).toLocaleString();
}

function envelopeValue(envelope: Record<string, unknown>, key: string, fallback: string) {
  const value = envelope[key];
  if (typeof value === "string" && value.trim()) {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return fallback;
}

function envelopeList(envelope: Record<string, unknown>, key: string) {
  const value = envelope[key];
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((item): item is string => typeof item === "string");
}

function policyLabel(envelope: Record<string, unknown>, key: string, t: (key: string) => string) {
  const policy = envelope.policy;
  if (!policy || typeof policy !== "object" || Array.isArray(policy)) {
    return t("policy.notSet");
  }
  const value = (policy as Record<string, unknown>)[key];
  return typeof value === "boolean" ? (value ? t("policy.allowed") : t("policy.blocked")) : t("policy.notSet");
}

function capacityWidth(snapshot: Backpressure) {
  const total = snapshot.nodes.reduce((sum, node) => sum + node.concurrency, 0);
  const used = snapshot.nodes.reduce((sum, node) => sum + node.active_runs, 0);
  if (!total) {
    return 0;
  }
  return Math.min(100, Math.round((used / total) * 100));
}

function refreshTaskQueries(task?: Task, runId?: string) {
  void queryClient.invalidateQueries({ queryKey: ["runs"] });
  void queryClient.invalidateQueries({ queryKey: ["runs", task?.id] });
  void queryClient.invalidateQueries({ queryKey: ["workflow", task?.id] });
  void queryClient.invalidateQueries({ queryKey: ["tasks", task?.project_id] });
  void queryClient.invalidateQueries({ queryKey: ["audit-logs"] });
  void queryClient.invalidateQueries({ queryKey: ["approvals"] });
  if (runId) {
    void queryClient.invalidateQueries({ queryKey: ["run-events", runId] });
    void queryClient.invalidateQueries({ queryKey: ["artifacts", runId] });
  }
}

function lines(value: string) {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

function submit(event: FormEvent, fn: () => void) {
  event.preventDefault();
  fn();
}
