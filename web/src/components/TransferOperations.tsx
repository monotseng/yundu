import {
  Alert,
  Button,
  Card,
  Col,
  Empty,
  Form,
  Modal,
  Row,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";
import { adminAPI } from "@/api/admin";
import { directionText, statusText } from "@/utils/display";

export default function TransferOperations({
  integrations,
}: {
  integrations: any[];
}) {
  const [channels, setChannels] = useState<any[]>([]),
    [jobs, setJobs] = useState<any[]>([]),
    [direction, setDirection] = useState<string>();
  const load = () =>
    Promise.all([
      adminAPI.channels().then((v) => setChannels(v.items)),
      adminAPI.transfers().then((v) => setJobs(v.items)),
    ]).catch(() => {});
  useEffect(() => {
    load();
    const timer = window.setInterval(load, 5000);
    return () => window.clearInterval(timer);
  }, []);
  const current = channels.find((v) => v.direction === direction),
    published = integrations.filter(
      (v) => v.status === "PUBLISHED" && v.current_version_id,
    );
  const save = async (v: any) => {
    try {
      await adminAPI.publishChannel(direction!, {
        ...v,
        expected_version: current?.version || 0,
      });
      message.success("交换渠道已发布");
      setDirection(undefined);
      load();
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  return (
    <Space direction="vertical" className="settings-section-stack" style={{ width: "100%" }} size={16}>
      <Row gutter={16} className="settings-summary">
        <Col span={8}><Card><span>已配置渠道</span><strong>{channels.length} / 2</strong></Card></Col>
        <Col span={8}><Card><span>运行中任务</span><strong>{jobs.filter(v => ["PENDING", "RUNNING"].includes(v.status)).length}</strong></Card></Col>
        <Col span={8}><Card><span>异常任务</span><strong>{jobs.filter(v => ["FAILED", "DEAD", "QUARANTINED"].includes(v.status)).length}</strong></Card></Col>
      </Row>
      {published.length < 2 && <Alert type="warning" showIcon message="交换渠道依赖两侧已发布存储" description="请先在“集成配置”中完成办公域与生产域对象存储的测试和发布。" />}
      <Card
        className="enterprise-card"
        title="交换渠道"
        extra={
          <Space>
            <Button onClick={() => setDirection("PROD_TO_OFFICE")}>
              配置生产到办公
            </Button>
            <Button onClick={() => setDirection("OFFICE_TO_PROD")}>
              配置办公到生产
            </Button>
          </Space>
        }
      >
        <Table
          rowKey="direction"
          pagination={false}
          dataSource={channels}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未发布交换渠道" /> }}
          columns={[
            {
              title: "方向",
              dataIndex: "direction",
              render: directionText,
            },
            { title: "源存储版本", dataIndex: "source_storage_version_id", ellipsis: true },
            { title: "目标存储版本", dataIndex: "target_storage_version_id", ellipsis: true },
            {
              title: "目标杀毒",
              dataIndex: "antivirus_enabled",
              render: (v) => <Tag color={v ? "success" : "default"}>{v ? "已启用" : "未启用"}</Tag>,
            },
            {
              title: "状态",
              dataIndex: "status",
              render: (v) => <Tag color="success">{statusText(v)}</Tag>,
            },
            { title: "版本", dataIndex: "version" },
          ]}
        />
      </Card>
      <Card className="enterprise-card" title="传输任务" extra={<Space><Typography.Text type="secondary">每 5 秒自动刷新</Typography.Text><Button onClick={load}>立即刷新</Button></Space>}>
        <Table
          rowKey="id"
          dataSource={jobs}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无传输任务" /> }}
          columns={[
            { title: "文件", dataIndex: "filename" },
            { title: "请求", dataIndex: "request_id" },
            {
              title: "状态",
              dataIndex: "status",
              render: (v) => (
                <Tag
                  color={
                    v === "SUCCEEDED"
                      ? "success"
                      : ["QUARANTINED", "DEAD"].includes(v)
                        ? "error"
                        : "processing"
                  }
                >
                  {statusText(v)}
                </Tag>
              ),
            },
            { title: "尝试", dataIndex: "attempts" },
            { title: "错误码", dataIndex: "last_error_code" },
            {
              title: "操作",
              render: (_, row) => (
                <Button
                  size="small"
                  disabled={!["FAILED", "DEAD"].includes(row.status)}
                  onClick={() =>
                    adminAPI
                      .retryTransfer(row.id)
                      .then(load)
                      .catch((e) => message.error(e.message))
                  }
                >
                  重试
                </Button>
              ),
            },
          ]}
        />
      </Card>
      <Modal
        open={!!direction}
        title={`配置${directionText(direction)}渠道`}
        footer={null}
        destroyOnClose
        onCancel={() => setDirection(undefined)}
      >
        <Form
          layout="vertical"
          className="enterprise-form"
          initialValues={{
            source_storage_version_id: current?.source_storage_version_id,
            target_storage_version_id: current?.target_storage_version_id,
            antivirus_enabled: current?.antivirus_enabled || false,
          }}
          onFinish={save}
        >
          <Form.Item
            name="source_storage_version_id"
            label="源存储已发布版本"
            rules={[{ required: true }]}
          >
            <Select
              options={published.map((v) => ({
                value: v.current_version_id,
                label: `${v.name} · ${v.zone}`,
              }))}
            />
          </Form.Item>
          <Form.Item
            name="target_storage_version_id"
            label="目标存储已发布版本"
            rules={[{ required: true }]}
          >
            <Select
              options={published.map((v) => ({
                value: v.current_version_id,
                label: `${v.name} · ${v.zone}`,
              }))}
            />
          </Form.Item>
          <Form.Item
            name="antivirus_enabled"
            label="目标侧再次杀毒"
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
          <Button block type="primary" htmlType="submit">
            发布渠道配置
          </Button>
        </Form>
      </Modal>
    </Space>
  );
}
