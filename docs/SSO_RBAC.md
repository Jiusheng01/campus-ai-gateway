# SSO 与 RBAC

生产环境的管理员认证不在 Campus AI Gateway 内重新实现完整 OIDC/JWT 登录体系，而是放在成熟的 OIDC/OAuth 反向认证组件之后，例如 **oauth2-proxy**。

## 1. 模式

### 账号密码登录（session，默认）

```text
ADMIN_AUTH_MODE=session
```

管理员账号保存在 `gateway_admin_user` 表，密码只存 bcrypt 哈希，明文不落库、不进日志、不进审计。
登录成功后服务端在 Redis 生成会话，浏览器只持有会话 ID，可在任意设备单独注销；停用或降权某个账号
会在下一个请求立刻生效，而不是等到会话过期。

首次启动（表中没有任何账号）时打开 `http://<host>:8080/admin-ui` 会进入初始化向导：

- 若已配置 `ADMIN_TOKEN`，初始化时必须填写该 Token 作为一次性所有权凭证
- 密码长度至少 10 位
- 账号创建后 `POST /admin/setup` 永久返回 409，无法再被用来添加第二个后门

也可以跳过向导，在启动前用环境变量自动建号：

```text
ADMIN_BOOTSTRAP_USERNAME=admin
ADMIN_BOOTSTRAP_PASSWORD=<至少 10 位>
```

相关接口：

```text
GET  /admin/auth-config   # 公开；返回认证模式与是否需要初始化
POST /admin/login         # {username,password} -> {token,user,roles,expires_at}
POST /admin/logout        # 注销当前会话
POST /admin/setup         # 首次建号
```

`session` 模式下若同时配置了 `ADMIN_TOKEN`，该 Token 仍可作为应急（break-glass）凭据使用，
以便账号表损坏时仍能进入控制面。

### 开发模式（共享 Token）

```text
ADMIN_AUTH_MODE=token
ADMIN_TOKEN=...
```

该 Token 会映射为 `super_admin`。

### 生产 / 校园 SSO

```text
Campus IdP
  -> oauth2-proxy（OIDC Session）
  -> Reverse Proxy
  -> Campus AI Gateway trusted-header auth
  -> RBAC
```

Gateway 配置：

```text
ADMIN_AUTH_MODE=trusted-header
ADMIN_PROXY_SECRET=<long random shared secret>
ADMIN_USER_HEADER=X-Forwarded-User
ADMIN_ROLES_HEADER=X-Forwarded-Groups
```

可信认证代理必须添加：

```text
X-Campus-Auth-Secret: <same ADMIN_PROXY_SECRET>
X-Forwarded-User: <stable campus account>
X-Forwarded-Groups: gateway_admin,auditor
```

启用 `trusted-header` 后，不要把 Gateway 管理端口直接暴露给用户网络。只有可信反向代理应该能访问该端口。

## 2. 角色

- `super_admin`：全部管理权限
- `gateway_admin`：Provider / Model / Route / ThinkWatch 同步 / 模型测试
- `tenant_admin`：Tenant / API Key 管理
- `auditor`：状态、请求日志、管理审计、安全事件
- `operator`：运行状态和模型测试

已认证用户可通过：

```text
GET /admin/whoami
```

查看解析后的身份和角色。

## 3. 学校 IdP 最终接入

Gateway 本身不绑定具体学校 IdP。

最终部署时，需要在 oauth2-proxy 中配置：

- 学校 Issuer URL
- Client ID
- Client Secret
- Callback URL
- Group / Claim 映射

这些值属于真实部署环境，因此不会写入仓库。

所以真实校园 SSO 登录属于**最终环境验收项**，不能仅靠 CI 宣称已经完成生产验证。
