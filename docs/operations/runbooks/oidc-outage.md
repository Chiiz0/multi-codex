# OIDC Outage Runbook

## Symptoms

- API or MCP returns `401` for valid users.
- Login callback fails or IdP JWKS fetch fails.
- `api.auth_denied` or `mcp.auth_denied` spikes in audit logs.

## Immediate Actions

1. Confirm `/healthz` is healthy for API and MCP.
2. Check IdP status, JWKS URL reachability, issuer, audience, and clock skew.
3. Do not switch production to local auth. Production validation is designed to
   reject that fallback.

## Recovery

1. Restore IdP/JWKS connectivity or roll back the OIDC config secret to the
   last working version.
2. Restart API and MCP Gateway after config correction.
3. Test login, bearer-token exchange, and browser logout.

## Evidence

Record failed trace IDs, IdP incident reference, config version, recovery time,
and audit rows used to confirm restoration.
