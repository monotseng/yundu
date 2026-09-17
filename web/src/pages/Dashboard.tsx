import {
  ArrowRightOutlined,
  CheckCircleOutlined,
  ClockCircleOutlined,
  FileAddOutlined,
  InboxOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import {
  Button,
  Card,
  Col,
  Empty,
  Row,
  Space,
  Statistic,
  Table,
  Tag,
  Typography,
} from "antd";
import { useNavigate } from "@umijs/renderer-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { requestAPI } from "@/api/requests";
import { workflowAPI } from "@/api/workflows";
import { downloadAPI } from "@/api/downloads";
import { formatDateTime } from "@/utils/datetime";
import { classificationText, directionText, statusText } from "@/utils/display";
import { useI18n } from "@/i18n";

const { Title, Text } = Typography;
const processingStatuses = new Set([
  "SCANNING",
  "CHECKING",
  "IN_REVIEW",
  "APPROVED",
  "TRANSFER_PENDING",
  "COPYING",
]);

export default function Dashboard() {
  const { language, t } = useI18n();
  const navigate = useNavigate();
  const [requests, setRequests] = useState<any[]>([]);
  const [tasks, setTasks] = useState<any[]>([]);
  const [downloads, setDownloads] = useState<any[]>([]);
  const [permissions, setPermissions] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    const [me, ownRequests, approvalTasks, availableDownloads] =
      await Promise.allSettled([
        fetch("/api/v1/auth/me", { credentials: "same-origin" }).then(
          async (response) =>
            response.ok ? response.json() : Promise.reject(),
        ),
        requestAPI.list(),
        workflowAPI.tasks(),
        downloadAPI.available(),
      ]);
    if (me.status === "fulfilled") setPermissions(me.value.permissions || []);
    setRequests(
      ownRequests.status === "fulfilled" ? ownRequests.value.items : [],
    );
    setTasks(
      approvalTasks.status === "fulfilled" ? approvalTasks.value.items : [],
    );
    setDownloads(
      availableDownloads.status === "fulfilled"
        ? availableDownloads.value.items
        : [],
    );
    setLoading(false);
  }, []);

  useEffect(() => {
    load();
    const timer = window.setInterval(load, 30000);
    const refresh = () => document.visibilityState === "visible" && load();
    document.addEventListener("visibilitychange", refresh);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [load]);

  const summary = useMemo(
    () => ({
      drafts: requests.filter((item) => item.status === "DRAFT").length,
      processing: requests.filter((item) => processingStatuses.has(item.status))
        .length,
      approvals: tasks.filter((item) => item.status === "PENDING").length,
      downloads: downloads.reduce(
        (count, item) =>
          count +
          (item.files?.filter((file: any) => !file.delivered).length || 0),
        0,
      ),
    }),
    [requests, tasks, downloads],
  );
  const canCreate = permissions.includes("request.create");
  const allowed = (...values: string[]) =>
    values.some((value) => permissions.includes(value));
  const cards = [
    {
      title: t("我的草稿"),
      value: summary.drafts,
      icon: <FileAddOutlined />,
      note: t("等待补充并提交"),
      path: "/requests",
      accessible: allowed("request.read.own"),
    },
    {
      title: t("处理中"),
      value: summary.processing,
      icon: <ClockCircleOutlined />,
      note: t("检查、审批或传输中"),
      path: "/requests",
      accessible: allowed("request.read.own"),
    },
    {
      title: t("待我审批"),
      value: summary.approvals,
      icon: <CheckCircleOutlined />,
      note: t("按任务逐项办理"),
      path: "/approvals",
      accessible: allowed(
        "approval.act",
        "approval.group",
        "approval.department",
        "approval.security",
      ),
    },
    {
      title: t("待领取文件"),
      value: summary.downloads,
      icon: <InboxOutlined />,
      note: t("仅在目标网络入口显示"),
      path: "/downloads",
      accessible: allowed("download.own"),
    },
  ];

  return (
    <div>
      <div className="page-heading">
        <div>
          <Title level={3}>{t("首页概览")}</Title>
          <Text type="secondary">
            {t("汇总我的交换申请、审批任务与待领取文件")}
          </Text>
        </div>
        <Space>
          <Button icon={<ReloadOutlined />} loading={loading} onClick={load}>
            {t("刷新")}
          </Button>
          {canCreate && (
            <Button
              type="primary"
              icon={<FileAddOutlined />}
              onClick={() => navigate("/requests/new")}
            >
              {t("新建交换")}
            </Button>
          )}
        </Space>
      </div>
      <Row gutter={[12, 12]} className="stat-row">
        {cards.map((card) => (
          <Col xs={24} sm={12} xl={6} key={card.title}>
            <Card
              hoverable={card.accessible}
              onClick={() => card.accessible && navigate(card.path)}
            >
              <Statistic
                title={card.title}
                value={card.value}
                prefix={card.icon}
                loading={loading}
              />
              <Text type="secondary">{card.note}</Text>
            </Card>
          </Col>
        ))}
      </Row>
      <Card
        title={t("最近申请")}
        extra={
          <Button type="link" onClick={() => navigate("/requests")}>
            {t("查看全部")} <ArrowRightOutlined />
          </Button>
        }
        className="main-card"
      >
        {requests.length ? (
          <Table
            rowKey="id"
            loading={loading}
            dataSource={requests.slice(0, 5)}
            pagination={false}
            columns={[
              { title: t("申请编号"), dataIndex: "request_no" },
              {
                title: t("方向"),
                dataIndex: "direction",
                render: (value) => (
                  <Tag color="blue">{directionText(value)}</Tag>
                ),
              },
              {
                title: t("密级"),
                dataIndex: "classification",
                render: classificationText,
              },
              {
                title: t("状态"),
                dataIndex: "status",
                render: (value) => (
                  <Tag
                    color={
                      value === "READY"
                        ? "success"
                        : value === "REJECTED"
                          ? "error"
                          : "default"
                    }
                  >
                    {statusText(value)}
                  </Tag>
                ),
              },
              {
                title: t("文件"),
                render: (_, row) =>
                  language === "en-US"
                    ? `${row.file_count} files`
                    : `${row.file_count} 个`,
              },
              {
                title: t("创建时间"),
                dataIndex: "created_at",
                render: formatDateTime,
              },
            ]}
          />
        ) : (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={t(
              loading
                ? "正在加载"
                : allowed("request.read.own")
                  ? "尚无交换申请"
                  : "当前角色未配置申请查看权限",
            )}
          />
        )}
        <div className="safety-note">
          <Space>
            <Tag color="cyan">{t("数据联动")}</Tag>
            <Text>
              {t(
                "统计数据每 30 秒刷新，并在重新进入页面时同步；文件仅能在目标网络入口领取。",
              )}
            </Text>
          </Space>
        </div>
      </Card>
    </div>
  );
}
