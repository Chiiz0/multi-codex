import { createContext, ReactNode, useContext, useMemo } from "react";
import type { AuthContext } from "./api";

export type Permission =
  | "*"
  | "users:read"
  | "users:write"
  | "organizations:read"
  | "organizations:write"
  | "projects:read"
  | "projects:write"
  | "repositories:write"
  | "tasks:write"
  | "runs:read"
  | "runs:write"
  | "approvals:write"
  | "nodes:read"
  | "nodes:write"
  | "skills:write"
  | "audit:read";

type AccessValue = {
  auth?: AuthContext;
  has: (permission: Permission | string) => boolean;
  hasAny: (permissions: Array<Permission | string>) => boolean;
};

const AccessContext = createContext<AccessValue>({
  has: () => false,
  hasAny: () => false
});

export function AccessProvider({ auth, children }: { auth?: AuthContext; children: ReactNode }) {
  const value = useMemo<AccessValue>(
    () => ({
      auth,
      has: (permission) => hasPermission(auth, permission),
      hasAny: (permissions) => permissions.some((permission) => hasPermission(auth, permission))
    }),
    [auth]
  );

  return <AccessContext.Provider value={value}>{children}</AccessContext.Provider>;
}

export function useAccess() {
  return useContext(AccessContext);
}

export function hasPermission(auth: AuthContext | undefined, permission: Permission | string) {
  if (!auth) {
    return false;
  }
  return auth.permissions.includes("*") || auth.permissions.includes(permission);
}

export function visiblePermissions(auth: AuthContext | undefined) {
  if (!auth) {
    return [];
  }
  if (auth.permissions.includes("*")) {
    return ["*"];
  }
  return auth.permissions;
}

export function projectRole(auth: AuthContext | undefined, projectId?: string) {
  if (!auth || !projectId) {
    return "";
  }
  return auth.project_memberships.find((membership) => membership.project_id === projectId)?.role ?? "";
}
