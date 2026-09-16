import { ReloadOutlined, SearchOutlined } from "@ant-design/icons";
import { Avatar, Button, Card, Col, DatePicker, Form, Input, Row, Select, Space, Table, Tag, Typography, message } from "antd";
import { useState } from "react";
import { auditAPI } from "@/api/m8";
import { formatDateTime } from "@/utils/datetime";
import { directionText, statusText } from "@/utils/display";

const { RangePicker } = DatePicker;
const directionOptions = [
  { value: "OFFICE_TO_PROD", label: "办公 → 生产" },
  { value: "PROD_TO_OFFICE", label: "生产 → 办公" },
];
const resultOptions = [
  { value: "SUCCESS", label: "成功" },
  { value: "DENIED", label: "拒绝" },
  { value: "FAILED", label: "失败" },
];

export default function Audit() {
  const [items, setItems] = useState<any[]>([]);
  const [next, setNext] = useState<number>();
  const [loading, setLoading] = useState(false);
  const [form] = Form.useForm();
  const search = async (values: any, cursor?: number) => {
    if (!values.range) { message.warning("审计查询必须指定时间窗"); return; }
    const query = new URLSearchParams({ from: values.range[0].toISOString(), to: values.range[1].toISOString(), limit: "100" });
    ["actor_id", "request_id", "action", "result", "direction"].forEach(key => values[key] && query.set(key, values[key]));
    if (cursor) query.set("cursor", String(cursor));
    setLoading(true);
    try {
      const result = await auditAPI.events(query);
      setItems(cursor ? [...items, ...result.items] : result.items);
      setNext(result.has_more ? result.next_cursor : undefined);
    } catch (error) { message.error((error as Error).message); }
    finally { setLoading(false); }
  };
  const reset = () => { form.resetFields(); setItems([]); setNext(undefined); };
  return <div className="settings-page">
    <div className="page-heading"><div><Typography.Title level={3}>审计中心</Typography.Title><Typography.Text type="secondary">按时间窗和业务维度检索审计元数据，不提供文件正文</Typography.Text></div></div>
    <Card className="enterprise-card audit-filter-card">
      <div className="filter-card-heading"><div><strong>审计查询</strong><span>时间范围为必填项，更多条件可组合使用</span></div></div>
      <Form form={form} layout="vertical" onFinish={values => search(values)}>
        <Row gutter={[16, 0]}>
          <Col xs={24} md={14} xl={8}><Form.Item name="range" label="发生时间" rules={[{ required: true, message: "请选择查询时间范围" }]}><RangePicker showTime format="YYYY-MM-DD HH:mm:ss" allowClear style={{ width: "100%" }} /></Form.Item></Col>
          <Col xs={24} md={10} xl={4}><Form.Item name="direction" label="交换方向"><Select allowClear placeholder="全部方向" options={directionOptions} /></Form.Item></Col>
          <Col xs={24} md={12} xl={4}><Form.Item name="result" label="执行结果"><Select allowClear placeholder="全部结果" options={resultOptions} /></Form.Item></Col>
          <Col xs={24} md={12} xl={8}><Form.Item name="action" label="操作类型"><Input allowClear placeholder="例如 REQUEST.SUBMIT" /></Form.Item></Col>
          <Col xs={24} md={12} xl={8}><Form.Item name="request_id" label="申请 ID"><Input allowClear placeholder="输入完整申请 ID" /></Form.Item></Col>
          <Col xs={24} md={12} xl={8}><Form.Item name="actor_id" label="操作者 ID"><Input allowClear placeholder="输入用户或服务账号 ID" /></Form.Item></Col>
          <Col xs={24} xl={8} className="audit-filter-actions"><Form.Item label=" "><Space><Button type="primary" htmlType="submit" icon={<SearchOutlined />} loading={loading}>查询</Button><Button icon={<ReloadOutlined />} onClick={reset}>重置</Button></Space></Form.Item></Col>
        </Row>
      </Form>
    </Card>
    <Card className="enterprise-card" title="查询结果" extra={<Typography.Text type="secondary">共 {items.length} 条当前结果</Typography.Text>}>
      <Table loading={loading} rowKey="id" dataSource={items} pagination={false} scroll={{ x: 1120 }} columns={[
        { title: "发生时间", dataIndex: "occurred_at", width: 180, render: formatDateTime },
        { title: "操作类型", dataIndex: "action", width: 180 },
        { title: "结果", dataIndex: "result", width: 90, render: value => <Tag color={value === "SUCCESS" ? "success" : value === "DENIED" ? "warning" : "error"}>{statusText(value)}</Tag> },
        { title: "方向", dataIndex: "direction", width: 140, render: directionText },
        { title: "申请 ID", dataIndex: "request_id", width: 220, ellipsis: true },
        { title: "操作者", width: 200, render: (_, row) => row.actor_name ? <div className="user-identity"><Avatar size={30} className="emoji-avatar">{row.actor_avatar || "👤"}</Avatar><div className="table-primary"><strong>{row.actor_name}</strong><span>{row.actor_username || "实名用户"}</span></div></div> : <Typography.Text type="secondary">{row.actor_id ? `系统账号 · ${row.actor_id.slice(0, 8)}` : "系统服务"}</Typography.Text> },
        { title: "事件哈希", dataIndex: "event_hash", width: 260, ellipsis: true },
      ]} />
      {next && <div className="table-load-more"><Button loading={loading} onClick={() => search(form.getFieldsValue(), next)}>加载更多</Button></div>}
    </Card>
  </div>;
}
