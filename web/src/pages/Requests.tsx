import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  Modal,
  Space,
  Steps,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";
import { Link } from "@umijs/renderer-react";
import { requestAPI } from "@/api/requests";
import { formatDateTime } from "@/utils/datetime";
import { formatFileSize } from "@/utils/filesize";
import {
  checkTypeText,
  classificationText,
  directionText,
  statusText,
} from "@/utils/display";
const nodeStatus = (v: string) =>
  v === "ACTIVE"
    ? "process"
    : ["APPROVED", "COMPLETED"].includes(v)
      ? "finish"
      : v === "REJECTED"
        ? "error"
        : "wait";
export default function Requests() {
  const [items, setItems] = useState<any[]>([]),
    [checks, setChecks] = useState<any[]>(),
    [progress, setProgress] = useState<any>();
  const load = () =>
    requestAPI
      .list()
      .then((v) => setItems(v.items))
      .catch((e) => message.error(e.message));
  useEffect(() => {
    load();
  }, []);
  const submit = (row: any) =>
    Modal.confirm({
      title: "提交并冻结申请？",
      content: "提交后文件版本、哈希、用途和杀毒策略将被冻结，不能再修改。",
      okText: "确认提交",
      onOk: () =>
        requestAPI
          .submit(row.id, row.version)
          .then(() => {
            message.success("已进入来源权威检查");
            load();
          })
          .catch((e) => message.error(e.message)),
    });
  const showChecks = (id: string) =>
    requestAPI
      .checks(id)
      .then((v) => setChecks(v.items))
      .catch((e) => message.error(e.message));
  const showProgress = (row: any) =>
    requestAPI
      .progress(row.id)
      .then((v) => setProgress({ ...v, request_no: row.request_no }))
      .catch((e) => message.error(e.message));
  return (
    <div>
      <div className="page-heading">
        <div>
          <Typography.Title level={3}>我的申请</Typography.Title>
          <Typography.Text type="secondary">
            跟踪本人申请的检查、审批和传输进度
          </Typography.Text>
        </div>
        <Link to="/requests/new">
          <Button type="primary">新建交换</Button>
        </Link>
      </div>
      <Card>
        <Table
          rowKey="id"
          dataSource={items}
          columns={[
            { title: "申请编号", dataIndex: "request_no" },
            {
              title: "方向",
              dataIndex: "direction",
              render: (v) => <Tag color="blue">{directionText(v)}</Tag>,
            },
            {
              title: "密级",
              dataIndex: "classification",
              render: classificationText,
            },
            {
              title: "状态",
              dataIndex: "status",
              render: (v) => (
                <Tag
                  color={
                    v === "IN_REVIEW"
                      ? "processing"
                      : v === "REJECTED_CHECK" ||
                          v === "SCAN_FAILED" ||
                          v === "REJECTED"
                        ? "error"
                        : v === "READY"
                          ? "success"
                          : "default"
                  }
                >
                  {statusText(v)}
                </Tag>
              ),
            },
            {
              title: "文件",
              render: (_, v) => `${v.file_count} 个 / ${formatFileSize(v.total_bytes)}`,
            },
            {
              title: "操作",
              width: 280,
              render: (_, row) => (
                <Space>
                  <Button
                    size="small"
                    disabled={row.status !== "DRAFT" || row.file_count === 0}
                    onClick={() => submit(row)}
                  >
                    提交检查
                  </Button>
                  <Button size="small" onClick={() => showProgress(row)}>
                    流程状态
                  </Button>
                  <Button size="small" onClick={() => showChecks(row.id)}>
                    检查报告
                  </Button>
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <Drawer
        width={720}
        open={!!progress}
        onClose={() => setProgress(undefined)}
        title={`申请流程 · ${progress?.request_no || ""}`}
      >
        <Descriptions
          bordered
          size="small"
          column={2}
          items={[
            {
              key: "request",
              label: "申请状态",
              children: <Tag>{statusText(progress?.request_status)}</Tag>,
            },
            {
              key: "workflow",
              label: "流程状态",
              children: progress?.workflow_status
                ? statusText(progress.workflow_status)
                : "尚未启动",
            },
          ]}
        />
        {progress?.nodes?.length ? (
          <Steps
            style={{ marginTop: 28 }}
            direction="vertical"
            current={Math.max(
              0,
              progress.nodes.findIndex((v: any) => v.status === "ACTIVE"),
            )}
            items={progress.nodes.map((v: any) => ({
              title: v.name || v.id,
              status: nodeStatus(v.status),
              description: (
                <Space direction="vertical" size={2}>
                  <Typography.Text type="secondary">
                    {statusText(v.status)}
                  </Typography.Text>
                  {v.due_at && v.status === "ACTIVE" && (
                    <Typography.Text type="secondary">
                      截止：{formatDateTime(v.due_at)}
                    </Typography.Text>
                  )}
                  {v.completed_at && (
                    <Typography.Text type="secondary">
                      完成：{formatDateTime(v.completed_at)}
                    </Typography.Text>
                  )}
                </Space>
              ),
            }))}
          />
        ) : (
          <Alert
            style={{ marginTop: 20 }}
            type="info"
            showIcon
            message="审批流程尚未启动"
            description="草稿提交后将先完成文件检查，通过后自动进入已绑定的审批流程。"
          />
        )}
      </Drawer>
      <Drawer
        width={760}
        open={!!checks}
        onClose={() => setChecks(undefined)}
        title="来源文件检查报告"
      >
        {checks?.length === 0 && (
          <Alert type="info" showIcon message="尚无检查结果" />
        )}
        <Table
          rowKey={(v) => v.file_id + v.check_type}
          dataSource={checks}
          pagination={false}
          columns={[
            { title: "文件", dataIndex: "filename" },
            { title: "检查", dataIndex: "check_type", render: checkTypeText },
            {
              title: "必需",
              dataIndex: "required",
              render: (v) => <Tag>{v ? "是" : "否"}</Tag>,
            },
            {
              title: "结果",
              dataIndex: "status",
              render: (v) => (
                <Tag
                  color={
                    v === "PASSED"
                      ? "success"
                      : v === "SKIPPED_DISABLED"
                        ? "default"
                        : "error"
                  }
                >
                  {statusText(v)}
                </Tag>
              ),
            },
            { title: "检查大小", dataIndex: "checked_bytes", render: formatFileSize },
            {
              title: "完整覆盖",
              dataIndex: "coverage_complete",
              render: (v) => (v ? "是" : "否"),
            },
          ]}
        />
        {checks?.some((v) => v.status === "SKIPPED_DISABLED") && (
          <Alert
            style={{ marginTop: 16 }}
            type="warning"
            showIcon
            message="杀毒未启用"
            description="该项未执行病毒扫描，不能解释为无病毒；文本、类型与哈希检查不受影响。"
          />
        )}
      </Drawer>
    </div>
  );
}
