import { BellOutlined, CheckCircleOutlined, FileTextOutlined, ReloadOutlined } from "@ant-design/icons";
import { Badge, Button, Card, Empty, Segmented, Space, Tag, Typography, message } from "antd";
import { useNavigate } from "@umijs/renderer-react";
import { useEffect, useMemo, useState } from "react";
import { notificationAPI } from "@/api/m8";
import { formatDateTime } from "@/utils/datetime";

const eventLabels: Record<string, string> = {
  APPROVAL_PENDING: "审批待办",
  REQUEST_READY: "文件可领取",
  REQUEST_REJECTED: "申请被拒绝",
  TRANSFER_COMPLETED: "传输完成",
};

export default function Notifications() {
  const navigate = useNavigate();
  const [items, setItems] = useState<any[]>([]);
  const [filter, setFilter] = useState("全部");
  const [loading, setLoading] = useState(true);
  const [reading, setReading] = useState<string>();
  const load = async () => {
    setLoading(true);
    try { setItems((await notificationAPI.list()).items); }
    catch (error) { message.error((error as Error).message); }
    finally { setLoading(false); }
  };
  useEffect(() => { load(); }, []);
  const unread = items.filter(item => !item.read_at).length;
  const visible = useMemo(() => filter === "未读" ? items.filter(item => !item.read_at) : items, [filter, items]);
  const markRead = async (item: any) => {
    setReading(item.id);
    try {
      await notificationAPI.read(item.id);
      setItems(current => current.map(value => value.id === item.id ? { ...value, read_at: new Date().toISOString() } : value));
    } catch (error) { message.error((error as Error).message); }
    finally { setReading(undefined); }
  };
  const icon = (type: string) => type === "APPROVAL_PENDING" ? <CheckCircleOutlined /> : type.includes("REQUEST") || type.includes("TRANSFER") ? <FileTextOutlined /> : <BellOutlined />;

  return <div className="notification-page">
    <div className="page-heading"><div><Typography.Title level={3}>通知中心</Typography.Title><Typography.Text type="secondary">集中查看审批、交换和文件领取相关的站内消息</Typography.Text></div><Button icon={<ReloadOutlined />} loading={loading} onClick={load}>刷新</Button></div>
    <Card className="enterprise-card notification-panel" title={<Space><span>站内通知</span>{unread > 0 && <Badge count={unread} overflowCount={99} />}</Space>} extra={<Segmented size="small" value={filter} onChange={value => setFilter(String(value))} options={[{ label: `全部 ${items.length}`, value: "全部" }, { label: `未读 ${unread}`, value: "未读" }]} />}>
      {visible.length ? <div className="notification-list">{visible.map(item => <article key={item.id} className={`notification-item ${item.read_at ? "is-read" : "is-unread"}`}>
        <div className="notification-icon">{icon(item.event_type)}</div>
        <div className="notification-content">
          <div className="notification-title-row"><div><Typography.Text strong>{item.title}</Typography.Text>{!item.read_at && <Badge status="processing" text="未读" />}</div><Typography.Text type="secondary">{formatDateTime(item.created_at)}</Typography.Text></div>
          <Typography.Paragraph className="notification-body">{item.body}</Typography.Paragraph>
          <div className="notification-meta"><Tag bordered={false}>{eventLabels[item.event_type] || "系统通知"}</Tag>{item.request_id && <Typography.Text type="secondary">关联申请 {item.request_id.slice(0, 12)}</Typography.Text>}</div>
        </div>
        <div className="notification-actions"><Space direction="vertical" size={6}>{!item.read_at && <Button size="small" type="primary" ghost loading={reading === item.id} onClick={() => markRead(item)}>标为已读</Button>}{item.request_id && <Button size="small" type="link" onClick={() => navigate("/requests")}>查看申请</Button>}</Space></div>
      </article>)}</div> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={loading ? "正在加载通知" : filter === "未读" ? "没有未读通知" : "暂无通知"} />}
    </Card>
    <Typography.Text className="notification-footnote" type="secondary">站内通知与企业微信等外部消息通道相互独立，外部发送失败不会影响站内消息。</Typography.Text>
  </div>;
}
