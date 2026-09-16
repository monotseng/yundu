export const zhCN = {
  status: {
    ACTIVE: "启用", PUBLISHED: "已发布", DRAFT: "草稿", DISABLED: "停用", REVOKED: "已撤销",
    PENDING_ACTIVATION: "待激活", MFA_BIND_REQUIRED: "待绑定验证器", MFA_REBIND_REQUIRED: "待重新绑定验证器",
    PENDING: "待处理", WAITING: "等待中", RUNNING: "处理中", PAUSED: "已暂停", ISSUED: "已签发",
    STARTED: "进行中", COMPLETED: "已完成", COMPLETE: "已完成", SUCCEEDED: "已完成", FAILED: "失败",
    DEAD: "已终止", INTERRUPTED: "已中断", CANCELLED: "已取消", EXPIRED: "已过期", QUARANTINED: "已隔离",
    APPROVED: "已通过", REJECTED: "已拒绝", IN_REVIEW: "审批中", PENDING_APPROVAL: "待审批",
    PENDING_REVIEW: "待复核", WORKFLOW_BLOCKED: "流程阻塞", SCANNING: "文件检查中", CHECKING: "检查中",
    REJECTED_CHECK: "文件检查未通过", CHECK_FAILED: "检查失败", CHECK_PASSED: "检查通过", SCAN_FAILED: "扫描失败",
    TRANSFER_PENDING: "等待传输", COPYING: "复制中", COPIED: "复制完成", READY: "可领取",
    PARTIALLY_DELIVERED: "部分已领取", DELIVERED: "已领取", UPLOADED: "已上传", RESERVED: "已预留",
    SUCCESS: "成功", ERROR: "错误", PASSED: "通过", DENIED: "已拒绝", SKIPPED_DISABLED: "未启用，已跳过", OPEN: "待处理",
    ACKNOWLEDGED: "已确认", CLOSED: "已关闭", VALIDATED: "已校验", BLOCKED: "已阻塞", BOUND: "已绑定",
  },
  direction: { OFFICE_TO_PROD: "办公 → 生产", PROD_TO_OFFICE: "生产 → 办公" },
  classification: { INTERNAL: "内部", SENSITIVE: "敏感", HIGH: "高敏", CONFIDENTIAL: "机密", RESTRICTED: "受限", PUBLIC: "公开" },
  zone: { OFFICE: "办公域", PRODUCTION: "生产域", UNKNOWN: "未知安全域", GLOBAL: "全局" },
  integrationType: {
    S3_STORAGE: "S3 对象存储", LLM: "大语言模型", HTTP_CONNECT_PROXY: "HTTP CONNECT 代理",
    WECOM_BOT: "企业微信群机器人", SMTP: "邮件服务", MONITORING_TOKEN: "监控令牌",
  },
  checkType: {
    HASH: "哈希校验", INTEGRITY: "完整性校验", CLAMAV: "病毒扫描", VIRUS: "病毒扫描", TYPE: "文件类型校验", MIME: "文件类型校验",
    EXTENSION: "扩展名校验", ACTIVE_CONTENT: "活动内容检查", CONTROL_BYTE: "控制字符检查",
    BINARY_SIGNATURE: "二进制签名检查", CONTAINER: "容器文件检查", CONTENT: "内容检查",
  },
  taskMode: { SINGLE: "单人审批", ANY: "任一审批", ALL: "全部会签" },
} as const;

export type MessageNamespace = keyof typeof zhCN;
