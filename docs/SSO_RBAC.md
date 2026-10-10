# SSO and RBAC

Production admin authentication is designed to sit behind a mature OIDC/OAuth reverse-auth component such as **oauth2-proxy** instead of reimplementing OIDC/JWT validation inside Campus AI Gateway.

## Modes

### Development

```text
ADMIN_AUTH_MODE=token
ADMIN_TOKEN=...
```

The token is mapped to `super_admin`.

### Production / campus SSO

```text
Campus IdP
  -> oauth2-proxy (OIDC session)
  -> reverse proxy
  -> Campus AI Gateway trusted-header auth
  -> RBAC
```

Gateway settings:

```text
ADMIN_AUTH_MODE=trusted-header
ADMIN_PROXY_SECRET=<long random shared secret>
ADMIN_USER_HEADER=X-Forwarded-User
ADMIN_ROLES_HEADER=X-Forwarded-Groups
```

The reverse-auth layer must add:

```text
X-Campus-Auth-Secret: <same ADMIN_PROXY_SECRET>
X-Forwarded-User: <stable campus account>
X-Forwarded-Groups: gateway_admin,auditor
```

Do not expose the Gateway admin port directly when trusted-header mode is enabled. Only the trusted reverse proxy should reach it.

## Roles

- `super_admin`: all admin operations
- `gateway_admin`: Provider / Model / Route / ThinkWatch sync / model test
- `tenant_admin`: Tenant / API Key administration
- `auditor`: status, request logs, admin audit, security events
- `operator`: runtime status and model test

Authenticated users can inspect their resolved identity through:

```text
GET /admin/whoami
```

## Campus IdP final integration

The gateway side is IdP-agnostic. Final deployment must configure oauth2-proxy with the university issuer URL, client ID, client secret, callback URL and group/claim mapping. Those values are environment-specific and are intentionally not stored in this repository.

A real campus SSO login is therefore a final-environment acceptance item, not something CI can truthfully validate.
