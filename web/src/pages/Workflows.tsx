import { CheckCircleOutlined, DeleteOutlined, EditOutlined, PlusOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { Alert, Button, Card, Col, Drawer, Form, Input, InputNumber, Modal, Row, Select, Space, Table, Tag, Typography, message } from "antd";
import { useEffect, useMemo, useState } from "react";
import { workflowAPI } from "@/api/workflows";
import { directionText } from "@/utils/display";

type FlowNode = { id: string; type: string; name: string; responsibility?: string; resolver?: string; mode?: string; comment_required?: boolean; timeout_hours?: number };
type FlowGraph = { nodes: FlowNode[]; edges: { from: string; to: string }[] };
const requiredSteps: FlowNode[] = [
  { id: "group", type: "APPROVAL", name: "团队负责人审批", responsibility: "GROUP_MANAGER", resolver: "GROUP_MANAGER", mode: "SINGLE", comment_required: false, timeout_hours: 24 },
  { id: "security", type: "APPROVAL", name: "安全复核", responsibility: "SECURITY_OFFICER", resolver: "SECURITY_OFFICER", mode: "ANY", comment_required: true, timeout_hours: 24 },
  { id: "department", type: "APPROVAL", name: "部门责任审批", responsibility: "DEPARTMENT_MANAGER", resolver: "DEPARTMENT_MANAGER", mode: "SINGLE", comment_required: false, timeout_hours: 24 },
];
const makeGraph = (steps: FlowNode[]): FlowGraph => {
  const nodes = [{ id: "start", type: "START", name: "申请提交" }, ...steps, { id: "end", type: "END_APPROVED", name: "审批通过" }];
  return { nodes, edges: nodes.slice(0, -1).map((node, index) => ({ from: node.id, to: nodes[index + 1].id })) };
};

function FlowCanvas({ graph, editable, onEdit, onRemove }: { graph: FlowGraph; editable?: boolean; onEdit?: (node: FlowNode) => void; onRemove?: (id: string) => void }) {
  return <div className="flow-canvas"><div className="flow-track">{graph.nodes.map((node, index) => <div className="flow-step-wrap" key={node.id}>
    <div className={`flow-node flow-node-${node.type.toLowerCase()}`}>
      <div className="flow-node-icon">{node.type === "APPROVAL" ? <SafetyCertificateOutlined /> : <CheckCircleOutlined />}</div>
      <div className="flow-node-main"><strong>{node.name}</strong><span>{node.type === "APPROVAL" ? `${node.mode === "ANY" ? "任一审批" : "单人审批"} · ${node.timeout_hours || 24} 小时` : node.type === "START" ? "触发流程" : "流程完成"}</span></div>
      {editable && node.type === "APPROVAL" && <Space size={2} className="flow-node-actions"><Button type="text" size="small" icon={<EditOutlined />} onClick={() => onEdit?.(node)} /><Button type="text" danger size="small" icon={<DeleteOutlined />} onClick={() => onRemove?.(node.id)} /></Space>}
    </div>{index < graph.nodes.length - 1 && <div className="flow-connector"><span>↓</span></div>}
  </div>)}</div></div>;
}

export default function Workflows() {
  const [items, setItems] = useState<any[]>([]), [open, setOpen] = useState(false), [steps, setSteps] = useState<FlowNode[]>(requiredSteps);
  const [editing, setEditing] = useState<FlowNode>(), [validation, setValidation] = useState<any>();
  const [editingVersion, setEditingVersion] = useState<any>(), [bindings, setBindings] = useState<any[]>([]);
  const [form] = Form.useForm(), [nodeForm] = Form.useForm();
  const graph = useMemo(() => makeGraph(steps), [steps]);
  const load = () => Promise.all([workflowAPI.list().then(v => setItems(v.items)), workflowAPI.bindings().then(v => setBindings(v.items))]).catch(e => message.error(e.message));
  useEffect(() => { load(); }, []);
  const openNode = (node?: FlowNode) => { const next = node || { id: `approval_${Date.now()}`, type: "APPROVAL", name: "新增审批", responsibility: "SECURITY_OFFICER", resolver: "SECURITY_OFFICER", mode: "SINGLE", timeout_hours: 24, comment_required: false }; setEditing(next); nodeForm.setFieldsValue(next); };
  const saveNode = (values: FlowNode) => { const next = { ...editing!, ...values, type: "APPROVAL", resolver: values.responsibility }; setSteps(current => current.some(v => v.id === next.id) ? current.map(v => v.id === next.id ? next : v) : [...current, next]); setEditing(undefined); };
  const save = async (values: any) => { try { if (editingVersion) await workflowAPI.update(editingVersion.id, { graph, layout: {}, expected_version: editingVersion.version }); else await workflowAPI.create({ ...values, graph }); setOpen(false); setEditingVersion(undefined); form.resetFields(); setSteps(requiredSteps); load(); message.success(editingVersion ? "流程草稿已更新，请重新校验" : "流程草稿已创建"); } catch (e) { message.error((e as Error).message); } };
  const openCreate = () => { setEditingVersion(undefined); setSteps(requiredSteps); form.resetFields(); setOpen(true); };
  const openEdit = async (row: any) => { try { const detail = await workflowAPI.version(row.draft_version_id); setEditingVersion(detail); setSteps((detail.graph?.nodes || []).filter((v: FlowNode) => v.type === "APPROVAL").map((v: FlowNode) => v.name === "组负责人审批" ? { ...v, name: "团队负责人审批" } : v)); form.setFieldsValue({ code: row.code, name: row.name, description: row.description }); setOpen(true); } catch (e) { message.error((e as Error).message); } };
  const validate = async (id?: string) => { if (!id) { message.warning("当前流程没有可校验的草稿版本"); return; } try { const result = await workflowAPI.validate(id); setValidation(result); if (result.passed) { message.success("结构和安全基线校验通过"); load(); } } catch (e) { message.error((e as Error).message); } };
  const publish = async (id?: string) => { if (!id) { message.warning("当前流程没有可发布的草稿版本"); return; } try { await workflowAPI.publish(id, "审批流程控制台发布"); message.success("版本已发布"); load(); } catch (e) { message.error((e as Error).message); } };
  const bind = async (row: any, direction: string) => { try { await workflowAPI.bind({ direction, scope_type: "GLOBAL", scope_id: "", workflow_version_id: row.current_version_id }); message.success("全局方向绑定已更新"); load(); } catch (e) { message.error((e as Error).message); } };
  return <div className="settings-page">
    <div className="page-heading"><div><Typography.Title level={3}>审批流程</Typography.Title><Typography.Text type="secondary">可视化编排审批节点，校验安全基线并按交换方向发布</Typography.Text></div><Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建审批流程</Button></div>
    <Row gutter={16} className="settings-summary"><Col span={8}><Card><span>流程总数</span><strong>{items.length}</strong></Card></Col><Col span={8}><Card><span>已发布</span><strong>{items.filter(v => v.status === "PUBLISHED").length}</strong></Card></Col><Col span={8}><Card><span>安全基线节点</span><strong>3</strong></Card></Col></Row>
    <Alert className="section-alert" type="info" showIcon message="成功路径必须覆盖三道责任审批" description="团队负责人、安全复核和部门责任审批必须按序出现；流程不允许循环、脚本或绕过安全节点。" />
    <Card className="enterprise-card" title="当前全局绑定" extra={<Typography.Text type="secondary">交换申请将按方向使用对应的已发布版本</Typography.Text>}><Row gutter={16}>{["OFFICE_TO_PROD", "PROD_TO_OFFICE"].map(direction => { const binding = bindings.find(v => v.direction === direction && v.scope_type === "GLOBAL" && v.enabled); return <Col span={12} key={direction}><div className={`binding-status ${binding ? "active" : "empty"}`}><div><Tag color={binding ? "success" : "warning"}>{binding ? "已绑定" : "未绑定"}</Tag><strong>{directionText(direction)}</strong></div>{binding ? <span>{binding.workflow_name}（{binding.workflow_code}） · V{binding.version_number}</span> : <span>该方向尚未配置全局审批流程</span>}</div></Col>; })}</Row></Card>
    <Card className="enterprise-card" title="流程版本" extra={<Typography.Text type="secondary">草稿通过校验后才可发布和绑定</Typography.Text>}>
      <Table rowKey="id" dataSource={items} pagination={{ pageSize: 10 }} columns={[
        { title: "流程", render: (_, r) => <div className="table-primary"><strong>{r.name}</strong><span>{r.code}</span></div> },
        { title: "版本状态", width: 140, render: (_, r) => r.draft_status ? <Tag color={r.draft_status === "VALIDATED" ? "success" : "processing"}>{r.draft_status === "VALIDATED" ? "草稿已校验" : "草稿待校验"}</Tag> : <Tag color="blue">已发布</Tag> },
        { title: "当前版本", dataIndex: "version", width: 100, render: v => `V${v || 1}` },
        { title: "操作", width: 500, render: (_, r) => <Space wrap><Button size="small" icon={<EditOutlined />} disabled={!r.draft_version_id} onClick={() => openEdit(r)}>编辑</Button><Button size="small" disabled={!r.draft_version_id} onClick={() => validate(r.draft_version_id)}>校验</Button><Button size="small" disabled={!r.draft_version_id && !r.current_version_id} onClick={() => workflowAPI.simulate(r.draft_version_id || r.current_version_id, { security_level: "HIGH", file_count: 2, total_size_bytes: 1024 }).then(v => Modal.info({ title: "模拟审批路径", content: v.selected_path.join(" → ") })).catch(e => message.error(e.message))}>模拟</Button><Button size="small" type="primary" disabled={r.draft_status !== "VALIDATED"} onClick={() => publish(r.draft_version_id)}>发布</Button><Button size="small" disabled={!r.current_version_id || !!r.draft_version_id} onClick={() => workflowAPI.clone(r.id).then(load)}>复制草稿</Button><Button size="small" disabled={!r.current_version_id} onClick={() => bind(r, "OFFICE_TO_PROD")}>绑定办公→生产</Button><Button size="small" disabled={!r.current_version_id} onClick={() => bind(r, "PROD_TO_OFFICE")}>绑定生产→办公</Button></Space> },
      ]} />
    </Card>
    <Drawer open={open} onClose={() => { setOpen(false); setEditingVersion(undefined); }} width={760} title={editingVersion ? "编辑审批流程草稿" : "新建审批流程"} extra={<Button type="primary" onClick={() => form.submit()}>{editingVersion ? "保存修改" : "保存草稿"}</Button>} destroyOnClose>
      <Form form={form} layout="vertical" className="enterprise-form" onFinish={save}>
        <div className="form-section"><div className="form-section-title"><strong>基本信息</strong><span>{editingVersion ? "已创建流程的标识信息不可在版本草稿中修改" : "用于识别、检索和版本管理"}</span></div><Row gutter={16}><Col span={10}><Form.Item name="code" label="流程编码" rules={[{ required: true }]}><Input disabled={!!editingVersion} placeholder="例如 FILE_EXCHANGE_STANDARD" /></Form.Item></Col><Col span={14}><Form.Item name="name" label="流程名称" rules={[{ required: true }]}><Input disabled={!!editingVersion} placeholder="例如 标准文件交换审批" /></Form.Item></Col><Col span={24}><Form.Item name="description" label="流程说明"><Input.TextArea disabled={!!editingVersion} rows={2} placeholder="说明适用范围和审批策略" /></Form.Item></Col></Row></div>
        <div className="form-section"><div className="form-section-title inline"><div><strong>流程编排</strong><span>节点按自上而下顺序执行</span></div><Button icon={<PlusOutlined />} onClick={() => openNode()}>添加审批节点</Button></div><FlowCanvas graph={graph} editable onEdit={openNode} onRemove={id => setSteps(v => v.filter(n => n.id !== id))} /></div>
      </Form>
    </Drawer>
    <Modal open={!!editing} onCancel={() => setEditing(undefined)} title={steps.some(v => v.id === editing?.id) ? "编辑审批节点" : "添加审批节点"} onOk={() => nodeForm.submit()} destroyOnClose><Form form={nodeForm} layout="vertical" className="enterprise-form" onFinish={saveNode}><Row gutter={16}><Col span={14}><Form.Item name="name" label="节点名称" rules={[{ required: true }]}><Input /></Form.Item></Col><Col span={10}><Form.Item name="timeout_hours" label="处理时限（小时）" rules={[{ required: true }]}><InputNumber min={1} max={720} style={{ width: "100%" }} /></Form.Item></Col><Col span={14}><Form.Item name="responsibility" label="审批责任" rules={[{ required: true }]}><Select options={[{ value: "GROUP_MANAGER", label: "团队负责人" }, { value: "SECURITY_OFFICER", label: "安全复核员" }, { value: "DEPARTMENT_MANAGER", label: "部门负责人" }]} /></Form.Item></Col><Col span={10}><Form.Item name="mode" label="审批方式"><Select options={[{ value: "SINGLE", label: "单人审批" }, { value: "ANY", label: "任一人审批" }]} /></Form.Item></Col><Col span={24}><Form.Item name="comment_required" label="审批意见"><Select options={[{ value: true, label: "必须填写" }, { value: false, label: "可选填写" }]} /></Form.Item></Col></Row></Form></Modal>
    <Modal open={!!validation} onCancel={() => setValidation(undefined)} onOk={() => setValidation(undefined)} title="流程校验结果">{validation?.passed ? <Alert type="success" showIcon message="校验通过" description="结构合法，所有成功路径均覆盖安全基线节点。" /> : <Alert type="error" showIcon message="校验未通过" description={<pre>{JSON.stringify(validation?.errors, null, 2)}</pre>} />}</Modal>
  </div>;
}
