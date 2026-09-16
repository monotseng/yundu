# 数据库、密钥与灾难恢复

## 恢复原则

MySQL、主密钥、对象存储版本和独立审计归档必须分别备份。只恢复数据库不能恢复主密钥；只有主密钥也不能重建业务状态。恢复演练必须使用隔离网络和副本，禁止用生产 Webhook、SMTP 或 AI 凭据发送测试。

## 数据库恢复步骤

1. 停止云渡，阻断办公、生产和管理入口。
2. 将备份恢复到隔离 MySQL，校验 UTC、版本、外键、表数量、binlog 位点及 `schema_migrations`。
3. 用恢复后的配置执行迁移，再立即执行：

   ```bash
   ./yundu-linux-arm64 --config /etc/yundu/config.yaml --enter-recovery-mode='restore ticket/change id'
   ```

   该事务设置恢复冻结，阻断发布 worker 和新下载，撤销全部 session、登录/激活 challenge、活动 grant，清空资源 holder 并提升 fencing token；同时将 TOTP 登录冻结两分钟并写审计。
4. 对照独立审计归档核验恢复点之后的审批、撤销、安全事件和对象 version。不得凭恢复数据库中的旧状态直接继续发布。
5. 对两侧对象逐项核对 bucket/key/version/SHA256；不确定对象保持隔离，不将最新版本替代冻结版本。
6. 安全管理员用重新登录且五分钟内 MFA 的会话，在管理面填写核验说明和 `expected_version` 解除冻结。解除只恢复后续 worker，不复活旧 grant、session、challenge 或终态任务。
7. 小流量验证双门户授权、一次性 grant、审计和监控后再开放入口。

## 主密钥恢复与轮换

`YUNDU_MASTER_KEY` 必须由企业秘密系统备份并实施双人控制。丢失后已有 MFA、S3、Webhook、SMTP、AI 等密文不可解密，不能通过数据库推算；应保持服务关闭，从可信备份恢复。当前 V1.0 的在线轮换需先以新 key revision 重加密全部秘密并验证计数，再切换运行密钥，禁止直接替换环境变量造成历史密文不可读。

## 必须记录的演练证据

记录备份时间/位点、恢复耗时、数据差异、冻结状态、失效会话/grant 数量、审计归档校验、对象抽查、重新开放审批人及未通过项。没有实际执行不得填写“通过”。
