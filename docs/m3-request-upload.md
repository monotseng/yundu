# M3 申请与流式上传交付说明

M3 在单体服务中交付双方向申请、事务化容量预占、流式对象上传以及不可变对象版本登记。

## 行为边界

- `OFFICE_TO_PROD` 只能从办公门户创建和上传，仅接受 `.sql`、`.csv`，单文件不超过 30 MiB。
- `PROD_TO_OFFICE` 只能从生产门户创建和上传，单文件上限由 `production_file_bytes` 配置。
- 每单最多 5 个文件；办公到生产整单不超过 150 MiB。文件预占、申请计数和共享配额计数在一个 MySQL 事务内更新，并以申请 `expected_version` 防止并发超占。
- 文件原名只保存为展示元数据；对象 Key 使用申请 ID 和随机文件 ID，不包含原名。
- 文件正文经 `/data/v1` 流式写入来源门户已发布的 S3 版本，不在进程内完整缓冲。成功后保存精确 `Version ID`、SHA-256 和字节数。
- 上传会话绑定当前用户、申请、来源门户和已发布存储版本，30 分钟过期且只能消费一次。
- 创建申请、预占和取消上传要求 `Idempotency-Key`；相同载荷可重放结果，不同载荷或并发占用返回冲突。
- MySQL 连接参数显式设置 UTC 会话时区，避免数据库系统时区影响有效期和成员关系判断。

## 页面与接口

- “我的申请”连接真实本人申请列表。
- “新建交换”根据当前门户锁定方向，加载本人有效组和启用业务系统，支持最多五个附件顺序预占和上传。
- `GET /api/v1/bootstrap`
- `GET/POST /api/v1/requests`
- `POST /api/v1/requests/{request_id}/upload-sessions`
- `PUT /data/v1/upload-sessions/{upload_id}/content`
- `POST /api/v1/upload-sessions/{upload_id}/abort`

## 验收说明

真实 MySQL 并发容量测试由 6 个上传预占竞争同一申请，验收结果必须为 5 个成功、1 个配额拒绝。真实双对象存储正文上传需要企业对象存储环境及凭据；无该环境时仅标记待联调，不宣称通过。
