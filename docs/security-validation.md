# 安全验证与未关闭风险

## 已自动检查

- 本地密码使用 Argon2id 固定参数，TOTP 有时间步防重放；恢复没有万能码。
- SecretStore 使用 AES-256-GCM、随机 nonce 和上下文 AAD；API 不回显已存秘密。
- 文件上传按字节上限、后缀、文件头、全文 UTF-8 和控制字符处理，不执行 SQL。
- 下载绑定本人、目标门户、session 与一次性 grant，并按周期复核撤销状态。
- AI 只允许本人 INTERNAL 草稿的显式文字，拒绝 IP/localhost 目标，并在 DNS 解析后拒绝 private、loopback、link-local、multicast 和 metadata 地址；禁止重定向。
- 恢复模式 fail-closed：保护状态不可读时认证/下载返回 503；恢复后凭据失效且复制 worker 停止领取新任务。

## 上线前必须执行

1. 使用专用扫描器检查源代码、静态资源、二进制 strings、日志、trace、审计导出和错误响应中不存在真实秘密。
2. 从非可信入口伪造 portal header、Origin、Host、Forwarded/X-Forwarded-For，确认不会改变授权域。
3. 测试 CSRF、会话固定、Cookie 域隔离、暴力登录限流、并发 TOTP 重放和角色/组织范围越权。
4. 对 Proxy、Webhook、SMTP、AI Base URL 执行 metadata、IPv4/IPv6、DNS 重绑定、重定向和代理故障测试。
5. 对 30MiB/100MiB 慢上传下载、畸形 JSON、超长头、断流和优雅停机执行资源耗尽检查。
6. 以真实 MySQL 8.4 验证审计写失败时关键事务回滚、恢复冻结以及最小权限账号。

## 已知风险

- 前端构建依赖树仍有上游安全告警；生产只嵌入静态产物，但正式发布前必须完成依赖升级、SBOM 和重新审计。
- 企业微信真实 @、SMTP 到达、Zabbix 导入、WORM、双存储删除和网络 ACL 依赖企业环境，当前没有通过证据。
- 当前 Go HTTP 服务应只监听回环或受控内网，由企业 TLS 入口提供请求体/头大小及慢流策略；不能直接暴露公网。
