import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Form,
  Input,
  Modal,
  Row,
  Select,
  Space,
  Tag,
  Typography,
  Upload,
  message,
} from "antd";
import type { UploadFile } from "antd";
import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "@umijs/renderer-react";
import { requestAPI } from "@/api/requests";
import { directionText } from "@/utils/display";
import { formatFileSize } from "@/utils/filesize";
export default function NewRequest() {
  const [boot, setBoot] = useState<any>(),
    [files, setFiles] = useState<UploadFile[]>([]),
    [saving, setSaving] = useState(false),
    [draft, setDraft] = useState<any>(),
    [suggestion, setSuggestion] = useState<any>(),
    [readiness, setReadiness] = useState<any>();
  const [form] = Form.useForm();
  const navigate = useNavigate();
  const teamID = Form.useWatch("group_id", form),
    systemID = Form.useWatch("business_system_id", form),
    classification = Form.useWatch("classification", form),
    direction = boot?.directions?.[0],
    team = boot?.groups?.find((v: any) => v.id === teamID);
  const systems = useMemo(
    () =>
      (boot?.business_systems || []).filter(
        (v: any) =>
          v.status === "ACTIVE" &&
          (!team ||
            !v.department_id ||
            v.department_id === team.department_id) &&
          (!direction || v.allowed_directions?.includes(direction)),
      ),
    [boot, team, direction],
  );
  useEffect(() => {
    requestAPI
      .bootstrap()
      .then((v: any) => {
        setBoot(v);
        if (v.groups?.length === 1)
          form.setFieldValue("group_id", v.groups[0].id);
      })
      .catch((e) => message.error(e.message));
  }, []);
  useEffect(() => {
    if (!teamID || !direction) {
      setReadiness(undefined);
      return;
    }
    if (systemID && !systems.some((v: any) => v.id === systemID)) {
      form.setFieldValue("business_system_id", undefined);
      return;
    }
    const p = new URLSearchParams({ direction, group_id: teamID });
    if (systemID) p.set("business_system_id", systemID);
    requestAPI
      .readiness(p)
      .then(setReadiness)
      .catch((e) => message.error(e.message));
  }, [teamID, systemID, direction, systems]);
  const ensureDraft = async () => {
    const v = await form.validateFields([
      "group_id",
      "business_system_id",
      "classification",
      "purpose",
      "external_ticket",
    ]);
    if (v.classification !== "INTERNAL")
      throw new Error("AI 辅助仅允许 INTERNAL 草稿");
    if (!readiness?.ready)
      throw new Error("当前范围尚未完成存储通道或审批流程配置");
    if (draft) return draft;
    const created = await requestAPI.create({ ...v, direction });
    setDraft(created);
    return created;
  };
  const assist = async () => {
    try {
      const current = await ensureDraft();
      setSuggestion(
        await requestAPI.aiSuggestion(
          current.id,
          form.getFieldValue("purpose"),
        ),
      );
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const adopt = () => {
    form.setFieldValue("purpose", suggestion.text);
    setSuggestion(undefined);
  };
  const submit = async (v: any) => {
    if (!readiness?.ready) return message.error("当前范围未具备完整交换条件");
    setSaving(true);
    try {
      let req = draft;
      if (!req) req = await requestAPI.create({ ...v, direction });
      else {
        const updated = await requestAPI.updatePurpose(
          req.id,
          v.purpose,
          req.version,
        );
        req = { ...req, version: updated.version };
      }
      let version = req.version;
      for (const entry of files) {
        const file = entry.originFileObj as File,
          session = await requestAPI.reserve(req.id, {
            original_name: file.name,
            size_bytes: file.size,
            content_type: file.type || "application/octet-stream",
            expected_version: version,
          });
        version++;
        await requestAPI.upload(session.id, file);
      }
      message.success("申请草稿与附件已保存");
      navigate("/requests");
    } catch (e) {
      message.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };
  const office = boot?.portal_zone === "OFFICE",
    fileLimit = office
      ? boot?.limits.office_file_bytes
      : boot?.limits.production_file_bytes,
    total = files.reduce((n, v) => n + (v.size || 0), 0);
  const beforeAdd = (file: File) => {
    if (office && !/\.(sql|csv)$/i.test(file.name)) {
      message.error("办公到生产仅允许 SQL 或 CSV 文件");
      return Upload.LIST_IGNORE;
    }
    if (fileLimit && file.size > fileLimit) {
      message.error(`单文件不能超过 ${formatFileSize(fileLimit)}`);
      return Upload.LIST_IGNORE;
    }
    return false;
  };
  return (
    <div className="settings-page">
      <div className="page-heading">
        <div>
          <Typography.Title level={3}>新建文件交换申请</Typography.Title>
          <Typography.Text type="secondary">
            按团队、业务系统、存储通道和审批流程创建可审计草稿
          </Typography.Text>
        </div>
        {direction && <Tag color="blue">{directionText(direction)}</Tag>}
      </div>
      {boot && !boot.groups?.length && (
        <Alert
          showIcon
          type="error"
          message="当前用户未加入任何团队"
          description="请先在“组织与用户”中维护团队成员关系。"
        />
      )}
      <Card className="enterprise-card request-form-card" loading={!boot}>
        <Form
          form={form}
          layout="vertical"
          onFinish={submit}
          initialValues={{ classification: "INTERNAL" }}
        >
          <div className="form-section">
            <div className="form-section-title">
              <strong>1. 责任范围</strong>
              <span>业务系统按团队所属部门和允许方向自动过滤</span>
            </div>
            <Row gutter={20}>
              <Col span={8}>
                <Form.Item
                  name="group_id"
                  label="申请团队"
                  rules={[{ required: true }]}
                >
                  <Select
                    disabled={!!draft}
                    placeholder="请选择责任团队"
                    options={boot?.groups.map((x: any) => ({
                      value: x.id,
                      label: `${x.name}（${x.code}）`,
                    }))}
                  />
                </Form.Item>
              </Col>
              <Col span={8}>
                <Form.Item name="business_system_id" label="业务系统">
                  <Select
                    disabled={!!draft || !teamID}
                    allowClear
                    placeholder={teamID ? "请选择（可选）" : "请先选择团队"}
                    options={systems.map((x: any) => ({
                      value: x.id,
                      label: `${x.name}（${x.code}）`,
                    }))}
                  />
                </Form.Item>
              </Col>
              <Col span={8}>
                <Form.Item
                  name="classification"
                  label="信息密级"
                  rules={[{ required: true }]}
                >
                  <Select
                    disabled={!!draft}
                    options={[
                      { value: "INTERNAL", label: "内部" },
                      { value: "SENSITIVE", label: "敏感" },
                      { value: "HIGH", label: "高敏" },
                    ]}
                  />
                </Form.Item>
              </Col>
            </Row>
            {teamID && (
              <Alert
                showIcon
                type={readiness?.ready ? "success" : "warning"}
                message={
                  readiness?.ready
                    ? "当前范围已具备交换条件"
                    : "当前范围配置尚未打通"
                }
                description={
                  <Space wrap>
                    <Tag
                      color={
                        readiness?.source_storage_ready ? "success" : "error"
                      }
                    >
                      来源存储
                    </Tag>
                    <Tag
                      color={
                        readiness?.target_storage_ready ? "success" : "error"
                      }
                    >
                      目标存储
                    </Tag>
                    <Tag color={readiness?.channel_ready ? "success" : "error"}>
                      交换通道
                    </Tag>
                    <Tag
                      color={readiness?.workflow_ready ? "success" : "error"}
                    >
                      审批流程
                    </Tag>
                  </Space>
                }
              />
            )}
          </div>
          <div className="form-section">
            <div className="form-section-title">
              <strong>2. 交换说明</strong>
              <span>用途将随文件版本冻结并进入审批记录</span>
            </div>
            <Row gutter={20}>
              <Col span={16}>
                <Form.Item
                  name="purpose"
                  label="交换用途"
                  extra="说明文件内容、接收方、使用范围及预期结果"
                  rules={[
                    { required: true },
                    { min: 10, message: "请至少填写 10 个字符" },
                    { max: 2000 },
                  ]}
                >
                  <Input.TextArea rows={5} showCount maxLength={2000} />
                </Form.Item>
                <Button
                  disabled={classification !== "INTERNAL"}
                  onClick={assist}
                >
                  AI 辅助整理
                </Button>
              </Col>
              <Col span={8}>
                <Form.Item
                  name="external_ticket"
                  label="外部工单号"
                  rules={[{ max: 128 }]}
                >
                  <Input
                    disabled={!!draft}
                    placeholder="例如 CHG-20260915-001"
                  />
                </Form.Item>
                <Descriptions
                  size="small"
                  column={1}
                  bordered
                  items={[
                    {
                      key: "d",
                      label: "方向",
                      children: directionText(direction),
                    },
                    {
                      key: "t",
                      label: "团队",
                      children: team?.name || "待选择",
                    },
                    {
                      key: "s",
                      label: "系统",
                      children:
                        systems.find((v: any) => v.id === systemID)?.name ||
                        "未指定",
                    },
                  ]}
                />
              </Col>
            </Row>
          </div>
          <div className="form-section">
            <div className="form-section-title">
              <strong>3. 交换文件</strong>
              <span>上传后记录不可变 Version ID 和 SHA-256</span>
            </div>
            <Form.Item
              label={`附件（最多 ${boot?.limits.files_per_request || 5} 个）`}
              required
            >
              <Upload.Dragger
                multiple
                beforeUpload={beforeAdd}
                fileList={files}
                onChange={({ fileList }) =>
                  setFiles(
                    fileList.slice(0, boot?.limits.files_per_request || 5),
                  )
                }
                accept={office ? ".sql,.csv" : undefined}
              >
                <p className="ant-upload-text">点击或拖拽文件到此区域</p>
                <p className="ant-upload-hint">
                  {office
                    ? `仅接受 SQL、CSV；单文件不超过 ${formatFileSize(fileLimit)}`
                    : "文件将按来源安全域策略校验"}
                </p>
              </Upload.Dragger>
            </Form.Item>
            <Typography.Text
              type={
                office && total > (boot?.limits.office_request_bytes || 0)
                  ? "danger"
                  : "secondary"
              }
            >
              已选择 {files.length} 个文件，共 {formatFileSize(total)}
            </Typography.Text>
          </div>
          <div className="form-actions">
            <Space>
              <Button onClick={() => navigate("/requests")}>取消</Button>
              <Button
                type="primary"
                htmlType="submit"
                loading={saving}
                disabled={
                  !files.length ||
                  !readiness?.ready ||
                  (office && total > (boot?.limits.office_request_bytes || 0))
                }
              >
                保存草稿并上传
              </Button>
            </Space>
          </div>
        </Form>
      </Card>
      <Modal
        open={!!suggestion}
        title="AI 整理建议（不可信纯文本）"
        onCancel={() => setSuggestion(undefined)}
        onOk={adopt}
        okText="采用到表单"
      >
        <Alert
          type="warning"
          showIcon
          message="AI 不会自动保存、提交或改变团队、密级和附件。"
        />
        <Input.TextArea
          style={{ marginTop: 16 }}
          rows={8}
          value={suggestion?.text}
          onChange={(e) =>
            setSuggestion({ ...suggestion, text: e.target.value })
          }
        />
      </Modal>
    </div>
  );
}
