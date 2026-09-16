import {
  Alert,
  Button,
  Card,
  Drawer,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { DownloadOutlined, HistoryOutlined } from "@ant-design/icons";
import { useEffect, useState } from "react";
import { downloadAPI } from "@/api/downloads";
import { formatDateTime } from "@/utils/datetime";
import { directionText, statusText } from "@/utils/display";
import { formatFileSize } from "@/utils/filesize";

function nativeDownload(grant: string) {
  const form = document.createElement("form");
  form.method = "POST";
  form.action = "/data/v1/downloads";
  form.style.display = "none";
  const input = document.createElement("input");
  input.name = "grant";
  input.value = grant;
  form.appendChild(input);
  document.body.appendChild(form);
  form.submit();
  window.setTimeout(() => form.remove(), 1000);
}
export default function Downloads() {
  const [items, setItems] = useState<any[]>([]),
    [history, setHistory] = useState<any[]>();
  const load = () =>
    downloadAPI
      .available()
      .then((v) => setItems(v.items))
      .catch((e) => message.error(e.message));
  useEffect(() => {
    load();
    const timer = window.setInterval(load, 15000);
    return () => window.clearInterval(timer);
  }, []);
  const take = async (file: any, version: number) => {
    try {
      const grant = await downloadAPI.grant(file.id, version);
      nativeDownload(grant.grant);
      message.success("下载已开始；发送完成不代表浏览器已经保存文件");
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const openHistory = (requestID: string) =>
    downloadAPI
      .receipts(requestID)
      .then((result) => setHistory(result.items))
      .catch((error) => message.error(error.message));
  const fileTable = (request: any) => (
    <Table
      className="download-file-table"
      rowKey="id"
      size="small"
      pagination={false}
      dataSource={request.files}
      columns={[
        { title: "文件名", dataIndex: "filename" },
        {
          title: "大小",
          dataIndex: "size_bytes",
          width: 130,
          render: formatFileSize,
        },
        {
          title: "领取状态",
          width: 140,
          render: (_, file: any) => (
            <Tag color={file.delivered ? "success" : "processing"}>
              {file.delivered ? "已领取" : "待领取"}
            </Tag>
          ),
        },
        {
          title: "操作",
          width: 110,
          render: (_, file: any) => (
            <Button
              type="primary"
              size="small"
              icon={<DownloadOutlined />}
              onClick={() => take(file, request.version)}
            >
              领取
            </Button>
          ),
        },
      ]}
    />
  );
  return (
    <div>
      <div className="page-heading">
        <div>
          <Typography.Title level={3}>文件领取</Typography.Title>
          <Typography.Text type="secondary">
            按申请单集中展示，展开后领取当前目标网络入口中的文件
          </Typography.Text>
        </div>
        <Button onClick={load}>刷新</Button>
      </div>
      <Alert
        style={{ marginBottom: 16 }}
        type="info"
        showIcon
        message="点击申请单左侧的加号查看文件"
        description="每个文件使用独立的一次性领取授权；授权 60 秒内仅可启动一次，失败后可在有效期内重新领取。"
      />
      <Card>
        <Table
          rowKey="id"
          dataSource={items}
          expandable={{
            expandedRowRender: fileTable,
            rowExpandable: (row) => row.files?.length > 0,
          }}
          columns={[
            { title: "申请编号", dataIndex: "request_no" },
            {
              title: "方向",
              dataIndex: "direction",
              render: (value) => <Tag color="blue">{directionText(value)}</Tag>,
            },
            {
              title: "文件",
              render: (_, row) => {
                const total = row.files.reduce(
                  (sum: number, file: any) =>
                    sum + Number(file.size_bytes || 0),
                  0,
                );
                return `${row.files.length} 个文件 · 共 ${formatFileSize(total)}`;
              },
            },
            {
              title: "领取进度",
              render: (_, row) => {
                const delivered = row.files.filter(
                  (file: any) => file.delivered,
                ).length;
                return (
                  <Tag
                    color={
                      delivered === row.files.length
                        ? "success"
                        : delivered > 0
                          ? "warning"
                          : "processing"
                    }
                  >
                    {delivered} / {row.files.length}
                  </Tag>
                );
              },
            },
            {
              title: "有效期至",
              dataIndex: "expires_at",
              render: formatDateTime,
            },
            {
              title: "操作",
              width: 110,
              render: (_, row) => (
                <Button
                  size="small"
                  icon={<HistoryOutlined />}
                  onClick={() => openHistory(row.id)}
                >
                  领取记录
                </Button>
              ),
            },
          ]}
        />
      </Card>
      <Drawer
        width={680}
        open={!!history}
        onClose={() => setHistory(undefined)}
        title="领取与发送记录"
      >
        <Table
          rowKey="id"
          dataSource={history}
          pagination={false}
          columns={[
            { title: "文件", dataIndex: "filename" },
            {
              title: "状态",
              dataIndex: "status",
              render: (v) => (
                <Tag color={v === "COMPLETED" ? "success" : "error"}>
                  {statusText(v)}
                </Tag>
              ),
            },
            {
              title: "已发送大小",
              dataIndex: "bytes_sent",
              render: formatFileSize,
            },
            {
              title: "结束时间",
              dataIndex: "finished_at",
              render: formatDateTime,
            },
          ]}
        />
      </Drawer>
    </div>
  );
}
