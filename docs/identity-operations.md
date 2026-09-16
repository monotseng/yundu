# 身份认证运行手册

## 首次部署

1. 生成并在企业秘密管理系统中固定保存 32 字节随机主密钥，将其以 Base64 形式提供给 `YUNDU_MASTER_KEY`。主密钥不能随服务重启重新生成。
2. 执行迁移：`./yundu-linux-arm64 --migrate --config config.yaml`。
3. 仅在用户表为空时执行：`./yundu-linux-arm64 --bootstrap-admin admin --display-name '系统管理员' --config config.yaml`。
4. 将终端中仅显示一次的激活凭据通过企业已验证渠道交给本人。不要写入工单正文、聊天群或日志。
5. 用户从对应安全域入口访问 `/#/activate`，设置密码，使用 Microsoft Authenticator 的“其他账户”扫描二维码并输入六位验证码。
6. 绑定成功后重新访问 `/#/login`，依次完成密码与 TOTP 验证。

首次管理员同时获得全局 `ORG_ADMIN` 与 `SECURITY_ADMIN`，用于建立后续组织和安全职责。正式运行后，MFA 恢复仍强制由两名不同管理员完成。

## 会话

- 密码正确只产生 5 分钟 challenge，不能调用业务接口。
- 正式会话绝对期限 8 小时、空闲期限 30 分钟，并绑定签发门户安全域。
- 正式 HTTPS 使用 `__Host-yundu-session`，开发 HTTP 使用不带该保留前缀的 Cookie。
- “退出”撤销当前会话；“退出全部”、修改密码、账号停用和 MFA 恢复提升或校验 `session_version`，旧会话立即失效。

## MFA 恢复

1. 企业线下核验身份后，由 `ORG_ADMIN` 创建恢复申请并填写 10～1000 字说明。
2. 另一名 `SECURITY_ADMIN` 使用 5 分钟内完成 MFA 的会话审核；发起人、审核人和被恢复用户必须互不冲突。
3. 批准事务撤销目标用户全部会话、提升 `session_version`、失效旧 TOTP，并生成仅显示一次、30 分钟有效的重绑凭据。
4. 用户访问 `/#/activate?mode=rebind`，输入重绑凭据和原本地密码，绑定新验证器。
5. 新验证码确认后仍需重新登录。

数据库只保存激活、绑定、重绑及会话令牌的 SHA-256，不保存其明文；TOTP 秘钥使用 AES-256-GCM 并绑定用户 ID 加密。
