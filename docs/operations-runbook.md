# 云渡 V1.0 运行手册

## 单体程序

唯一发行程序为 `release/yundu-linux-arm64`。当前部署不依赖 Docker、Compose、Redis、消息队列或终端 Agent。

```bash
export YUNDU_DB_PASSWORD='由企业秘密系统注入'
export YUNDU_MASTER_KEY='至少 32 字节随机主密钥，由企业秘密系统注入'
./release/yundu-linux-arm64 --config /etc/yundu/config.yaml --check-config
./release/yundu-linux-arm64 --config /etc/yundu/config.yaml --migrate
./release/yundu-linux-arm64 --start --config /etc/yundu/config.yaml --pid-file /run/yundu/yundu.pid --log-file /var/log/yundu/yundu.log
./release/yundu-linux-arm64 --status --pid-file /run/yundu/yundu.pid
./release/yundu-linux-arm64 --stop --pid-file /run/yundu/yundu.pid
```

配置文件权限建议 `0640 root:yundu`，主密钥和数据库密码只通过配置中的 `${NAME}` 替换注入。不得将实际值写入配置、命令历史、日志或备份说明。

## 上线顺序

1. 校准应用、MySQL、对象存储、Authenticator 手机和 Zabbix 的 UTC 时间。
2. 以备份专用账号完成 MySQL 全量备份，记录 GTID/binlog 位点；验证恢复到隔离环境。
3. 执行 `--check-config` 与 `--migrate`，确认 schema version 为 16。
4. 首次部署用 `--bootstrap-admin` 获取一次性激活凭据，完成密码和真实手机 TOTP 绑定。
5. 分别配置办公/生产版本化对象存储，再发布方向通道；默认保持 ClamAV、AI 关闭。
6. 配置独立审计桶与签名秘密；企业微信、SMTP、AI 必须先用明确的合成目标测试，测试通过后才发布。
7. 创建 Zabbix 只读令牌并限制采集 CIDR；在真实 Zabbix 7.0 导入模板验证。
8. 依次验证 live、ready、登录、草稿、审批、复制和本人目标门户领取，再开放入口流量。

## 日常检查

- `/health/live` 只代表进程存活；`/health/ready` 同时检查 MySQL。
- 检查传输任务队龄、失败阶段、资源槽、旧监控快照、安全事件、审计日归档和对象删除任务。
- `ACCEPTED` 仅表示企业微信/SMTP 服务受理，不表示到达、已读或真实 @ 成功。
- AI 调用只记录模型、字符/Token、耗时和结果，不保存 prompt/response；配额未知时按保守上限计费。
- 轮换秘密时创建新引用并重新测试版本，不覆盖或回显旧秘密。

## 停机与升级

先从入口摘除实例并等待 ready 不再接流量，再执行 `--stop`。120 秒优雅窗口用于结束普通请求；文件流中断会保留可追踪状态，不能手工改成成功。升级前备份数据库并保存旧二进制；迁移只向前，不在生产执行 down migration。回滚应用前必须确认旧程序理解当前 schema，否则保持新程序并修复前进。

## 内容与审计

普通内容由生命周期 worker 按精确对象 version 删除，delete marker 不等于删除完成。隔离对象、未完成 multipart、候选版本和备份正文须纳入同一保留制度。审计在线保留目标为 365 天，只有独立归档验证通过且到期后才允许专用维护账号小批清理。是否具备 WORM 由存储侧证明，平台不自行宣称。
