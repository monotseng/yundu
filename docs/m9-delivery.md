# M9 AI、安全恢复与交付说明

M9 完成 V1.0 开发阶段收口。它提供可选 AI 文字辅助、恢复冻结、安全/容量验证方案、AC01～AC64 证据矩阵和运行手册；不把尚未取得的企业环境结果写成生产验收通过。

## 可选 AI 文字辅助

- AI 默认关闭，只有已测试并发布的 `LLM` 集成才启用。
- 仅原申请人自己的 `INTERNAL`、`DRAFT` 申请可调用。客户端必须显式提交最多 4000 字符的用途文字；服务端不读取或发送附件、文件名、样本、审批意见、密码、TOTP 或审计内容。
- 适配器明确实现 HTTPS `chat/completions` 兼容契约，固定模型、最多 1000 output tokens、30 秒超时且不自动重试。
- Base URL 不允许凭据、query、literal IP 或 localhost；连接前重新解析 DNS 并拒绝 private、loopback、link-local、multicast、unspecified 和 metadata 地址，禁止 HTTP 重定向。
- MySQL 实施每用户每日最多 20 次、月度保守 Token 预算和两个全局租约槽。供应商未返回 usage 时仍按输入估算加最大输出额度计费，不伪造零用量。
- 响应是不可信纯文本，页面先预览；用户点击“采用到草稿”后仍可编辑，只有后续明确保存才以乐观版本更新用途。AI 不能修改密级、方向、人员、附件、期限、状态或授权。
- `ai_invocations` 只记录用户/申请/模型版本、字符数、Token、耗时和结果码，不保存 prompt/response 全文。

## 数据库恢复保护

- 单体新增 `--enter-recovery-mode='原因/变更号'` 维护操作。
- 同一事务冻结发布和下载、撤销 session/login challenge/activation token/grant、释放 holder 并提升 fencing token，同时写审计；登录额外冻结两分钟，避免旧 TOTP 时间步回退重放。
- 恢复保护状态不可读取时，登录和下载 fail-closed 返回 503；复制 worker 在冻结期间不领取新任务。
- 解除冻结只允许安全管理员，要求处置说明、`expected_version` 和五分钟内 MFA；不会复活恢复前的 session、challenge、grant 或终态任务。

## 交付物

- 生产配置模板：[config.production.example.yaml](../configs/config.production.example.yaml)
- 运行手册：[operations-runbook.md](operations-runbook.md)
- 数据库与密钥恢复：[database-recovery.md](database-recovery.md)
- 容量验证方案：[performance-validation.md](performance-validation.md)
- 安全验证与风险：[security-validation.md](security-validation.md)
- 验收矩阵：[ac01-ac64-evidence.md](ac01-ac64-evidence.md)

真实 Microsoft Authenticator、MySQL 8.4、双版本化对象存储、ClamAV、企业微信 @、SMTP、WORM、Zabbix 7.0、30/60 人负载和旧备份恢复仍标记为待企业预生产联调。
