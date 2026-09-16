# M6 精确传输与整体发布交付说明

M6 将审批通过后的请求可靠地复制到目标安全域，并且只有全部文件完成目标复检后才整体发布。

## 一致性与故障恢复

- 审批结束事务同时冻结已发布渠道版本、创建每文件传输任务和 `TRANSFER_REQUESTED` Outbox 事件；没有已发布渠道或源版本不匹配时不会进入复制。
- worker 使用 MySQL `FOR UPDATE SKIP LOCKED` 领取任务；作业租约 90 秒、每 20 秒续约，并分别校验作业 fencing token 与 COPY 资源槽 token，过期 worker 不能提交结果。
- 每次尝试使用新的目标候选对象键。外部 S3 读取、上传、目标重开和 ClamAV 均不占用数据库事务。
- 源读取固定 `object_key + version_id`；上传后固定目标 `object_key + version_id` 重开，全量复算字节数、SHA-256 和方向对应的内容规则。
- 普通瞬时错误按 1、2、4、8、16 分钟退避，最多自动尝试 5 次。哈希不一致、目标内容复检失败或发现病毒直接隔离，不盲目重试。
- 每个文件仅在带有效 fencing token 的事务中写入精确目标版本和 TARGET 检查记录。所有文件成功后，请求才原子切换为 `READY` 并产生 `REQUEST_RELEASED` Outbox 事件。

## 管理入口

- “系统设置 → 交换与传输”发布生产到办公、办公到生产两个渠道，并显示传输任务、尝试次数和安全错误码。
- `GET/PUT /api/v1/admin/exchange-channels` 管理渠道；`GET /api/v1/admin/transfers` 查看任务；失败任务的重试接口要求 `Idempotency-Key`。
- 后台传输 worker 随 `yundu` 单体服务一起启动、随服务优雅停止，无独立守护进程或容器依赖。

真实双 S3/MinIO 与 ClamAV 的端到端复制仍需环境、桶版本控制和凭据后联调，本地开发结果不将该外部验收标记为通过。
