# 云渡工程约束

本仓库实现《云渡文件交换平台 产品需求与系统设计 V1.0 开发定稿（2026-09-15）》。交付包中的 Word 文档是唯一产品基线。

## 不可变边界

- Go 模块化单体；React + TypeScript + Ant Design 前端构建后嵌入同一个 Go 二进制。
- MySQL 8.4 保存业务状态、会话、审计、任务、租约和监控聚合；不引入 Redis、ClickHouse、消息中间件或微服务。
- 纯 Web 双向交换，不安装终端 Agent，不执行 SQL/脚本，不自动下发文件。
- 办公到生产只接受 `.sql`/`.csv`，单文件不超过 30 MiB、每单最多 5 个、合计不超过 150 MiB；必须逐字节完成 UTF-8、控制字节、二进制特征和哈希检查。
- 杀毒默认关闭，关闭时只能记录 `SKIPPED_DISABLED`，不能宣称扫描通过。
- 本地密码 + 标准 TOTP；不得加入万能验证码、默认密码或绕过 MFA 的恢复路径。
- 文件原名与 UUID 对象 key 分离；审批提交后冻结精确对象 version 与 SHA256。
- 只有申请人能从目标门户领取；管理员没有隐含下载权限。
- 管理员代办必须保留原办理人，以真实 `actor_id`、原因、权限范围和 MFA 时间审计。
- 所有秘密均不得写入日志、浏览器存储、URL、OpenAPI 示例或仓库。

## 工程规则

- HTTP handler、后台任务和适配器只能调用领域服务，不得直接散写领域状态。
- 外部 HTTP/S3/AI/通知调用不能放在数据库事务中。
- 所有状态变更接口使用乐观版本；关键写操作使用 `Idempotency-Key`。
- 时间在数据库和接口中统一为 UTC，列名统一使用 `*_at`。
- 配置通过 YAML 文件加载；环境变量只用于替换 `${NAME}` 形式的秘密，不维护第二套隐式配置。
- 新迁移只前进，不修改已发布迁移；集成测试使用真实 MySQL，不用 SQLite 替代。
- 页面沿用 dbadesk 的高密度控制台设计语言：左侧业务导航、状态 Tag、筛选表格、详情抽屉和明确下一步动作。

## 验证命令

```bash
go test ./...
go vet ./...
go run ./cmd/yundu-web --config configs/config.example.yaml --check-config
cd web && npm ci && npm run build && npm run typecheck
make build
./release/yundu-linux-arm64 --start --config configs/config.local.yaml --pid-file yundu.pid --log-file yundu.log
./release/yundu-linux-arm64 --stop --pid-file yundu.pid
```

真实 Microsoft Authenticator、双对象存储、企业微信群 @、Zabbix 7.0 导入以及 30/60 人负载测试必须保留环境与证据；没有企业环境时标记待联调，不得写成已通过。Docker/Compose 容器化与高可用拓扑推迟到生产性能部署阶段，当前不得成为单体服务启动依赖。
