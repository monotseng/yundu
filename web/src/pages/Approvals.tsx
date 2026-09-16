import {
  Button,
  Card,
  Form,
  Input,
  Modal,
  Select,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";
import { workflowAPI } from "@/api/workflows";
import { formatDateTime } from "@/utils/datetime";
import { statusText } from "@/utils/display";
export default function Approvals() {
  const [items, setItems] = useState<any[]>([]);
  const [task, setTask] = useState<any>();
  const load = () =>
    workflowAPI
      .tasks()
      .then((v) => setItems(v.items))
      .catch((e) => message.error(e.message));
  useEffect(() => {
    load();
  }, []);
  const decide = async (v: any) => {
    try {
      await workflowAPI.decide(task.id, {
        ...v,
        expected_version: task.version,
        checklist: [],
      });
      setTask(undefined);
      load();
      message.success("决定已记录");
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  return (
    <div>
      <div className="page-heading">
        <div>
          <Typography.Title level={3}>我的审批</Typography.Title>
          <Typography.Text type="secondary">
            实际任务、会签状态与办理截止时间
          </Typography.Text>
        </div>
      </div>
      <Card>
        <Table
          rowKey="id"
          dataSource={items}
          columns={[
            { title: "申请", dataIndex: "request_no" },
            { title: "节点", dataIndex: "node_id" },
            {
              title: "状态",
              dataIndex: "status",
              render: (v) => (
                <Tag color={v === "PENDING" ? "processing" : "default"}>
                  {statusText(v)}
                </Tag>
              ),
            },
            { title: "截止", dataIndex: "due_at", render: formatDateTime },
            {
              title: "操作",
              render: (_, r) => (
                <Button
                  type="primary"
                  size="small"
                  disabled={r.status !== "PENDING"}
                  onClick={() => setTask(r)}
                >
                  办理
                </Button>
              ),
            },
          ]}
        />
      </Card>
      <Modal
        open={!!task}
        footer={null}
        title="审批决定"
        onCancel={() => setTask(undefined)}
      >
        <Form layout="vertical" onFinish={decide}>
          <Form.Item name="decision" label="决定" rules={[{ required: true }]}>
            <Select options={[{value:'APPROVE',label:'同意'},{value:'REJECT',label:'拒绝'}]}/>
          </Form.Item>
          <Form.Item name="comment" label="意见（拒绝至少 5 个字符）">
            <Input.TextArea rows={4} />
          </Form.Item>
          <Button block type="primary" htmlType="submit">
            确认决定
          </Button>
        </Form>
      </Modal>
    </div>
  );
}
