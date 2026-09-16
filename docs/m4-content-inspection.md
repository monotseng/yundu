# M4 内容检查交付说明

M4 提供提交冻结、来源权威检查、可选 ClamAV 和逐文件检查报告。扫描由 MySQL 持久任务驱动，进程重启后仍可继续领取，不依赖内存队列。

## 检查规则

- 办公到生产：逐字节完整读取，只允许完整有效 UTF-8（含 ASCII），仅文件开头允许 UTF-8 BOM；拒绝 NUL、除 TAB/CR/LF 外的 C0 控制字符、DEL、截断码点以及 PE、ELF、ZIP、PNG、JPEG、PDF、GZIP 等二进制文件头。
- 生产到办公：允许 `txt/log/csv/json/sql/pdf/xlsx`；文本类型执行相同全文 UTF-8 检查；PDF 校验签名；XLSX 校验 OOXML 基本结构、条目和展开容量边界，并拒绝宏、嵌入内容与加密容器。
- 所有检查重新读取冻结的 `storage revision + bucket + object key + version id`，计算原始字节 SHA-256，并与上传阶段哈希和真实大小比较；不使用 ETag 代替哈希，也不回退读取最新对象。
- 合法 BOM、换行和制表符不会被修改；系统不转码、不修复文件，也不进行 SQL 语义或 CSV 业务内容分析。

## 杀毒语义

默认 `antivirus.enabled: false`。关闭时不连接 ClamAV，每个文件明确记录：

- `required=false`
- `status=SKIPPED_DISABLED`
- `checked_bytes=0`
- `coverage_complete=false`

该状态只表示“杀毒未启用”，不表示无病毒。启用时必须配置 `address`，通过 clamd `INSTREAM` 协议流式发送精确对象版本；只有 `PASSED` 可继续，检出、超时和不确定结果均阻断。

## 提交与状态

提交要求申请仍为本人来源门户草稿、`expected_version` 匹配、声明已确认，且所有文件均已完成并具有 Version ID 与 SHA-256。同一事务冻结规则版本、杀毒要求及文件计数，将状态改为 `SCANNING` 并创建唯一扫描任务。结果为：

- 所有必需检查通过：`IN_REVIEW`
- 明确规则不合格：`REJECTED_CHECK`
- 外部读取或扫描器技术故障重试耗尽：`SCAN_FAILED`

普通技术故障最多尝试 5 次；不会自动把启用的杀毒降级成关闭。

## 页面与接口

- `POST /api/v1/requests/{request_id}/submit`
- `GET /api/v1/requests/{request_id}/checks`
- “我的申请”支持提交冻结，并通过检查报告抽屉展示必需性、状态、覆盖字节和完整覆盖标识。

真实 ClamAV 病毒库与双对象存储扫描需要企业环境；无该环境时保持待联调状态。
