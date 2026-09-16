# M2 控制面交付说明

M2 提供组织与权限、受信门户边界、秘密存储以及版本化 S3 集成配置。所有功能随 `yundu` 单体二进制交付，不依赖 Docker 或 Compose。

## 已交付

- 部门、组、组成员、用户、业务系统领域服务与管理 API。
- 全局、部门、组三级 RBAC 范围，以及申请人、组负责人、部门负责人、安全审批员、审计员、系统运维员、组织管理员、安全管理员角色目录。
- 新用户一次性激活凭据；服务端只保存 SHA-256 摘要。
- AES-GCM SecretStore；API 不回显秘密明文，S3 配置只保存秘密引用。
- S3 存储草稿、真实连通探测和发布门禁。探测仅在配置前缀的 `_yundu_probe` 下写入随机对象，校验精确 Version ID 与 SHA-256 后删除同一版本，不读取业务对象。
- 生产环境仅信任配置 CIDR 内反向代理提供的门户安全域头；写请求同时校验当前门户 Origin。
- 高密度系统设置页面：用户、部门、组、业务系统、秘密与存储集成。

## 发布约束

存储版本只有在持久化的真实探测结果为 `PASSED` 后才能发布。办公与生产两套真实对象存储的联调证据需要企业环境，当前不将其标记为已通过。

生产配置必须为 `security.trust_portal_header_from` 设置明确的代理 CIDR，并启用 HTTPS 与安全 Cookie。秘密应通过 `${NAME}` 从运行环境注入配置文件。

## M2 API

- `/api/v1/admin/departments`、`/groups`、`/users`、`/roles`
- `/api/v1/admin/groups/{group_id}/members`
- `/api/v1/admin/users/{user_id}/roles`
- `/api/v1/admin/business-systems`
- `/api/v1/admin/secrets`
- `/api/v1/admin/integrations`、`/integrations/s3`
- `/api/v1/admin/integrations/{integration_id}/test`、`/publish`

## 验收结果（2026-09-15）

- `go test ./...`：通过。
- `go vet ./...`：通过。
- `npm run typecheck`、`npm run build`：通过。
- MySQL 8.4：迁移版本 1–6 已应用，角色目录已核对。
- `make build`：生成 `release/yundu-linux-arm64`，前端资源已嵌入。
- 二进制 `--check-config`、`--start`、就绪探针与 `--stop`：通过。
- 双对象存储真实凭据与权限：待企业环境联调。

