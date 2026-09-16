import { ApiOutlined, CloudServerOutlined, DatabaseOutlined, DeleteOutlined, EditOutlined, GatewayOutlined, KeyOutlined, MailOutlined, PlusOutlined, ReloadOutlined, RobotOutlined, SafetyCertificateOutlined, SearchOutlined, TeamOutlined } from "@ant-design/icons";
import {
  Alert,
  Avatar,
  Button,
  Card,
  Col,
  DatePicker,
  Form,
  Input,
  Modal,
  Popconfirm,
  Row,
  Segmented,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";
import { useLocation } from "@umijs/renderer-react";
import { adminAPI } from "@/api/admin";
import { aiAdminAPI, archiveAPI, monitoringAPI, outboundAPI } from "@/api/m8";
import TransferOperations from "@/components/TransferOperations";
import { formatDateTime } from "@/utils/datetime";
import { directionText, integrationTypeText, statusText, zoneText } from "@/utils/display";

const avatarEmojiOptions = [
  ["🐼", "熊猫"], ["🦊", "狐狸"], ["🐯", "老虎"], ["🦁", "狮子"],
  ["🐨", "考拉"], ["🐻", "棕熊"], ["🐻‍❄️", "北极熊"], ["🐰", "兔子"],
  ["🐶", "小狗"], ["🐱", "小猫"], ["🐵", "猴子"], ["🐧", "企鹅"],
  ["🦉", "猫头鹰"], ["🦅", "雄鹰"], ["🐺", "灰狼"], ["🦄", "独角兽"],
  ["🐬", "海豚"], ["🐳", "鲸鱼"], ["🦦", "水獭"], ["🦥", "树懒"],
  ["🐙", "章鱼"], ["🦋", "蝴蝶"],
].map(([value, name]) => ({ value, label: `${value}  ${name}` }));

export default function Settings() {
  const location = useLocation();
  const [settingsForm] = Form.useForm();
  const userScope = Form.useWatch("scope_type", settingsForm);
  const userDepartment = Form.useWatch("department_id", settingsForm);
  const [departments, setDepartments] = useState<any[]>([]);
  const [groups, setGroups] = useState<any[]>([]);
  const [users, setUsers] = useState<any[]>([]);
  const [roles, setRoles] = useState<any[]>([]);
  const [systems, setSystems] = useState<any[]>([]);
  const [integrations, setIntegrations] = useState<any[]>([]);
  const [monitoring, setMonitoring] = useState<any>({});
  const [monitoringTokens, setMonitoringTokens] = useState<any[]>([]);
  const [outbound, setOutbound] = useState<any[]>([]);
  const [archives, setArchives] = useState<any[]>([]);
  const [dialog, setDialog] = useState<string>();
  const [result, setResult] = useState<any>();
  const [submitting, setSubmitting] = useState(false);
  const [editingIntegration, setEditingIntegration] = useState<any>();
  const [editingGroup, setEditingGroup] = useState<any>();
  const [selectedUser, setSelectedUser] = useState<any>();
  const [integrationCategory, setIntegrationCategory] = useState("全部");
  const [integrationKeyword, setIntegrationKeyword] = useState("");
  const [orgKeyword, setOrgKeyword] = useState("");
  const load = () =>
    Promise.all([
      adminAPI
        .departments()
        .then((v) => setDepartments(v.items))
        .catch(() => {}),
      adminAPI
        .groups()
        .then((v) => setGroups(v.items))
        .catch(() => {}),
      adminAPI
        .users()
        .then((v) => setUsers(v.items))
        .catch(() => {}),
      adminAPI.roles().then(v => setRoles(v.items)).catch(() => {}),
      adminAPI
        .businessSystems()
        .then((v) => setSystems(v.items))
        .catch(() => {}),
      adminAPI
        .integrations()
        .then((v) => setIntegrations(v.items))
        .catch(() => {}),
      monitoringAPI.status().then(setMonitoring).catch(() => {}),
      monitoringAPI.tokens().then(v=>setMonitoringTokens(v.items)).catch(()=>{}),
      outboundAPI.list().then(v=>setOutbound(v.items)).catch(()=>{}),
      archiveAPI.list().then(v=>setArchives(v.items)).catch(()=>{}),
    ]);
  useEffect(() => {
    load();
  }, []);
  const submit = async (values: any) => {
    if (submitting) return;
    setSubmitting(true);
    try {
      let value: any;
      if (dialog === "department") {
        value = await adminAPI.createDepartment({ code: values.code, name: values.name });
        const managerRole = roles.find(v => v.code === "DEPARTMENT_MANAGER");
        if (managerRole) await Promise.all((values.manager_user_ids || []).map((userID: string) => adminAPI.assignRole(userID, { role_id: managerRole.id, scope_type: "DEPARTMENT", scope_id: value.id })));
      }
      if (dialog === "group") {
        if (editingGroup) {
          value = await adminAPI.updateGroup(editingGroup.id, { department_id: values.department_id, name: values.name, manager_user_ids: values.manager_user_ids, expected_version: editingGroup.version });
        } else {
          value = await adminAPI.createGroup({ department_id: values.department_id, code: values.code, name: values.name });
          await Promise.all((values.manager_user_ids || []).map((userID: string, index: number) => adminAPI.addGroupMember(value.id, { user_id: userID, is_manager: true, priority: index })));
        }
      }
      if (dialog === "user") {
        value = await adminAPI.createUser({ username: values.username, display_name: values.display_name, avatar_emoji: values.avatar_emoji });
        const userID = value.user.id;
        await Promise.all((values.role_ids || []).map((roleID: string) => adminAPI.assignRole(userID, { role_id: roleID, scope_type: values.scope_type, scope_id: values.scope_type === "DEPARTMENT" ? values.department_id : values.scope_type === "GROUP" ? values.group_id : "" })));
        if (values.group_id) await adminAPI.addGroupMember(values.group_id, { user_id: userID, is_manager: !!values.is_manager, priority: Number(values.priority || 0) });
      }
      if (dialog === "edit-user") value = await adminAPI.updateUser(selectedUser.id, { display_name: values.display_name, avatar_emoji: values.avatar_emoji, status: values.status, group_id: values.group_id, is_manager: values.is_manager, priority: Number(values.priority||0), expected_version: selectedUser.version });
      if (dialog === "mfa-reset") value = await adminAPI.initiateMFARecovery({ user_id: selectedUser.id, note: values.note });
      if (dialog === "mfa-approve") value = await adminAPI.approveMFARecovery(values.recovery_id, { note: values.note, expected_version: 1 });
      if (dialog === "system")
        value = await adminAPI.createBusinessSystem(values);
      if (dialog === "secret") value = await adminAPI.createSecret(values);
      if (dialog === "s3") {
        const rawEndpoint = String(values.endpoint).trim();
        const endpoint = rawEndpoint.replace(/^https?:\/\//i, "").replace(/\/+$/, "");
        let accessSecretID = editingIntegration?.version?.config?.access_key_secret_id;
        let secretSecretID = editingIntegration?.version?.config?.secret_key_secret_id;
        if (values.access_key) accessSecretID = (await adminAPI.createSecret({ name: `${values.name}-access-key`, purpose: "S3_ACCESS_KEY", value: values.access_key })).id;
        if (values.secret_key) secretSecretID = (await adminAPI.createSecret({ name: `${values.name}-secret-key`, purpose: "S3_SECRET_KEY", value: values.secret_key })).id;
        const payload = {
          name: values.name,
          zone: values.zone,
          expected_version: editingIntegration?.definition?.version,
          config: {
            endpoint,
            region: values.region || "",
            bucket: values.bucket,
            prefix: values.prefix || "",
            use_tls: /^https:\/\//i.test(rawEndpoint) ? true : /^http:\/\//i.test(rawEndpoint) ? false : values.use_tls,
            path_style: values.path_style,
            versioning_required: true,
            access_key_secret_id: accessSecretID,
            secret_key_secret_id: secretSecretID,
          },
        };
        value = editingIntegration ? await adminAPI.updateS3(editingIntegration.definition.id, payload) : await adminAPI.createS3(payload);
      }
      if(dialog === "monitoring-token") value=await monitoringAPI.create({name:values.name,allowed_cidrs:String(values.allowed_cidrs).split(/[\n,]+/).map(v=>v.trim()).filter(Boolean),expires_at:values.expires_at||null});
      if(dialog === "proxy") {
        let passwordSecretID = "";
        if (values.password) passwordSecretID = (await adminAPI.createSecret({ name: `${values.name}-proxy-password`, purpose: "PROXY_PASSWORD", value: values.password })).id;
        const payload = {name:values.name,password_secret_id:passwordSecretID,expected_version:editingIntegration?.row?.version,config:{proxy_url:values.proxy_url,username:values.username||'',allowed_hosts:['qyapi.weixin.qq.com'],allowed_ports:[443],connect_timeout_seconds:5,request_timeout_seconds:10}};
        value = editingIntegration ? await outboundAPI.updateProxy(editingIntegration.row.id, payload) : await outboundAPI.createProxy(payload);
      }
      if(dialog === "wecom") {
        let webhookSecretID = "";
        if (values.webhook) webhookSecretID = (await adminAPI.createSecret({ name: `${values.name}-wecom-webhook`, purpose: "WECOM_WEBHOOK", value: values.webhook })).id;
        const payload = {name:values.name,webhook_secret_id:webhookSecretID,expected_version:editingIntegration?.row?.version,config:{proxy_id:values.proxy_id}};
        value = editingIntegration ? await outboundAPI.updateWeCom(editingIntegration.row.id, payload) : await outboundAPI.createWeCom(payload);
      }
      if(dialog === "archive") value=await archiveAPI.create({from:values.range[0].toISOString(),to:values.range[1].toISOString(),storage_version_id:values.storage_version_id,signing_key_secret_id:values.signing_key_secret_id});
      if(dialog === "ai") {
        let apiKeySecretID = "";
        if (values.api_key) apiKeySecretID = (await adminAPI.createSecret({ name: `${values.name}-llm-api-key`, purpose: "LLM_API_KEY", value: values.api_key })).id;
        const payload = {name:values.name,api_key_secret_id:apiKeySecretID,expected_version:editingIntegration?.version,config:{base_url:values.base_url,model:values.model,daily_per_user:20,monthly_token_budget:Number(values.monthly_token_budget),timeout_seconds:30,max_input_chars:4000,max_output_tokens:1000}};
        value = editingIntegration ? await aiAdminAPI.update(editingIntegration.id, payload) : await aiAdminAPI.create(payload);
      }
      setDialog(undefined);
      setEditingIntegration(undefined);
      setEditingGroup(undefined);
      setSelectedUser(undefined);
      if (value?.activation_token || value?.rebind_token || (value?.id && ["secret", "mfa-reset"].includes(dialog || "")))
        setResult(value);
      message.success("保存成功");
      load();
    } catch (e) {
      message.error((e as Error).message);
    } finally {
      setSubmitting(false);
    }
  };
  const status = (v: string) => (
	<Tag color={v === "ACTIVE" || v === "PUBLISHED" ? "success" : "default"}>
	  {statusText(v)}
	</Tag>
  );
  const tabs = [
    {
      key: "users",
      label: "用户",
      children: (
        <Card
          extra={
            <Button type="primary" onClick={() => setDialog("user")}>
              创建用户
            </Button>
          }
        >
          <Table
            rowKey="id"
            dataSource={users}
            pagination={false}
            columns={[
              { title: "账号", dataIndex: "username" },
              { title: "显示名", dataIndex: "display_name" },
              { title: "状态", dataIndex: "status", render: status },
              { title: "身份验证", dataIndex: "mfa_state", render: v => <Tag color={v === "BOUND" ? "success" : "warning"}>{v === "BOUND" ? "已绑定" : "待绑定"}</Tag> },
            ]}
          />
        </Card>
      ),
    },
    {
      key: "departments",
      label: "组织架构",
      children: (
        <Space direction="vertical" style={{ width: "100%" }}>
          <Card
            title="部门"
            extra={
              <Button onClick={() => setDialog("department")}>新建部门</Button>
            }
          >
            <Table
              rowKey="id"
              dataSource={departments}
              pagination={false}
              columns={[
                { title: "编码", dataIndex: "code" },
                { title: "名称", dataIndex: "name" },
                { title: "状态", dataIndex: "status", render: status },
              ]}
            />
          </Card>
          <Card
            title="团队"
            extra={<Button onClick={() => setDialog("group")}>新建团队</Button>}
          >
            <Table
              rowKey="id"
              dataSource={groups}
              pagination={false}
              columns={[
                { title: "编码", dataIndex: "code" },
                { title: "名称", dataIndex: "name" },
                { title: "所属部门", dataIndex: "department_id", render: v => departments.find(d => d.id === v)?.name || v },
              ]}
            />
          </Card>
          <Card
            title="业务系统"
            extra={
              <Button onClick={() => setDialog("system")}>新建业务系统</Button>
            }
          >
            <Table
              rowKey="id"
              dataSource={systems}
              pagination={false}
              columns={[
                { title: "编码", dataIndex: "code" },
                { title: "名称", dataIndex: "name" },
                { title: "所属部门", dataIndex: "department_id", render: v => departments.find(d => d.id === v)?.name || "—" },
                {
                  title: "允许方向",
                  dataIndex: "allowed_directions",
                  render: (v: string[]) => v?.map(directionText).join("、"),
                },
                { title: "状态", dataIndex: "status", render: status },
              ]}
            />
          </Card>
        </Space>
      ),
    },
    {
      key: "integrations",
      label: "集成配置",
      children: (
        <Card
          extra={
            <Space>
              <Button onClick={() => setDialog("secret")}>录入秘密</Button>
              <Button type="primary" onClick={() => setDialog("s3")}>
                新建 S3 存储
              </Button>
            </Space>
          }
        >
          <Table
            rowKey="id"
            dataSource={integrations}
            pagination={false}
            columns={[
              { title: "名称", dataIndex: "name" },
              { title: "类型", dataIndex: "type", render: integrationTypeText },
              { title: "安全域", dataIndex: "zone", render: zoneText },
              { title: "状态", dataIndex: "status", render: status },
              {
                title: "操作",
                render: (_, row) => (
                  <Space>
                    <Button
                      size="small"
                      onClick={() =>
                        adminAPI
                          .testS3(row.id)
                          .then(() => {
                            message.success("精确版本探测通过");
                            load();
                          })
                          .catch((e) => message.error(e.message))
                      }
                    >
                      测试
                    </Button>
                    <Button
                      size="small"
                      disabled={row.status !== "DRAFT"}
                      onClick={() =>
                        adminAPI
                          .publish(row.id, row.version)
                          .then(() => load())
                          .catch((e) => message.error(e.message))
                      }
                    >
                      发布
                    </Button>
                  </Space>
                ),
              },
            ]}
          />
        </Card>
      ),
    },
    {
      key: "transfers",
      label: "交换与传输",
      children: <TransferOperations integrations={integrations} />,
    },
    {key:"monitoring",label:"Zabbix 监控",children:<Card title="只读 HTTP 监控" extra={<Space><Button href="/api/v1/admin/monitoring/zabbix-template">下载 Zabbix 7.0 模板</Button><Button type="primary" onClick={()=>setDialog('monitoring-token')}>签发令牌</Button></Space>}><Space style={{marginBottom:16}}><Tag color={monitoring.enabled?'success':'default'}>{monitoring.enabled?'已配置':'未配置'}</Tag><Typography.Text>快照：{monitoring.snapshot_valid?'有效':'不可用'}</Typography.Text><Typography.Text>接口 /api/v1/monitoring/zabbix</Typography.Text></Space><Table rowKey="id" dataSource={monitoringTokens} pagination={false} columns={[{title:'名称',dataIndex:'name'},{title:'允许采集源',dataIndex:'allowed_cidrs',render:(v:string[])=>v?.join('、')},{title:'最后采集',dataIndex:'last_used_at',render:v=>v?formatDateTime(v):'尚未采集'},{title:'状态',dataIndex:'enabled',render:v=><Tag color={v?'success':'default'}>{v?'有效':'已撤销'}</Tag>},{title:'操作',render:(_,v)=><Button danger size="small" disabled={!v.enabled} onClick={()=>monitoringAPI.revoke(v.id).then(load)}>撤销</Button>}]}/></Card>},
    {key:"outbound",label:"群通知集成",children:<Card extra={<Space><Button onClick={()=>setDialog('proxy')}>新建 CONNECT Proxy</Button><Button type="primary" onClick={()=>setDialog('wecom')}>新建企业微信群机器人</Button></Space>}><Typography.Paragraph type="secondary">V1.0 仅支持企业微信群机器人；Webhook 与代理密码加密保存，Proxy 失败不会回退直连。</Typography.Paragraph><Table rowKey="id" dataSource={outbound} pagination={false} columns={[{title:'名称',dataIndex:'name'},{title:'类型',dataIndex:'type',render:integrationTypeText},{title:'状态',dataIndex:'status',render:status},{title:'秘密',dataIndex:'secret_configured',render:v=>v?'已配置':'无'},{title:'操作',render:(_,v)=><Button size="small" disabled={v.status!=='DRAFT'} onClick={()=>outboundAPI.publish(v.id,v.version).then(load)}>发布</Button>}]}/></Card>},
    {key:"archives",label:"审计归档",children:<Card extra={<Button type="primary" onClick={()=>setDialog('archive')}>执行日归档</Button>}><Typography.Paragraph type="secondary">JSONL 写入独立版本化存储，记录事件范围、数量、SHA256 与独立密钥 HMAC 签名；是否具备 WORM 以存储侧配置为准。</Typography.Paragraph><Table rowKey="id" dataSource={archives} pagination={false} columns={[{title:'范围开始',dataIndex:'range_start',render:formatDateTime},{title:'事件数',dataIndex:'event_count'},{title:'SHA256',dataIndex:'sha256',ellipsis:true},{title:'对象',dataIndex:'object_key'},{title:'状态',dataIndex:'status',render:status}]}/></Card>},
    {key:"ai",label:"AI 辅助",children:<Card extra={<Button type="primary" onClick={()=>setDialog('ai')}>新建模型草稿</Button>}><Alert showIcon type="info" message="默认关闭；仅已发布的 LLM 集成可用于本人 INTERNAL 草稿" description="只发送用户明确确认的用途文字；不发送附件、文件名、审批意见或审计内容。每日每人最多 20 次，全局并发 2。"/><Table style={{marginTop:16}} rowKey="id" dataSource={integrations.filter(v=>v.type==='LLM')} pagination={false} columns={[{title:'名称',dataIndex:'name'},{title:'状态',dataIndex:'status',render:status},{title:'版本',dataIndex:'version'},{title:'操作',render:(_,v)=><Space><Button size="small" disabled={v.status!=='DRAFT'} onClick={()=>aiAdminAPI.test(v.id,'请将这段合成测试用途整理为一句专业说明。').then(()=>message.success('合成文字契约测试通过'))}>契约测试</Button><Button size="small" disabled={v.status!=='DRAFT'} onClick={()=>adminAPI.publish(v.id,v.version).then(load)}>发布</Button></Space>}]}/></Card>},
  ];
  const providers = [
    { key: "s3", name: "S3 / MinIO 对象存储", category: "存储", icon: <CloudServerOutlined />, tone: "blue", description: "按安全域配置版本化对象存储，用于交换载荷与审计归档。", count: integrations.filter(v => v.type === "S3_STORAGE").length, action: () => { setEditingIntegration(undefined); settingsForm.resetFields(); setDialog("s3"); } },
    { key: "proxy", name: "HTTP CONNECT Proxy", category: "网络", icon: <GatewayOutlined />, tone: "cyan", description: "为受控出站通知提供固定目的地址的网络代理。", count: outbound.filter(v => v.type === "HTTP_CONNECT_PROXY").length, action: () => { setEditingIntegration(undefined); settingsForm.resetFields(); setDialog("proxy"); } },
    { key: "wecom", name: "企业微信群机器人", category: "通知", icon: <ApiOutlined />, tone: "green", description: "将审批和交换状态推送至企业微信群，Webhook 加密保存。", count: outbound.filter(v => v.type === "WECOM_GROUP_BOT").length, action: () => { setEditingIntegration(undefined); settingsForm.resetFields(); setDialog("wecom"); } },
    { key: "smtp", name: "Email / SMTP", category: "通知", icon: <MailOutlined />, tone: "orange", description: "用于邮件通知的预留集成类型，后续版本开放配置。", count: outbound.filter(v => v.type === "SMTP").length, disabled: true },
    { key: "llm", name: "大语言模型", category: "智能", icon: <RobotOutlined />, tone: "purple", description: "接入 OpenAI 兼容模型，仅处理用户明确确认的非敏感文字。", count: integrations.filter(v => v.type === "LLM").length, action: () => { setEditingIntegration(undefined); settingsForm.resetFields(); setDialog("ai"); } },
  ];
  const filteredProviders = providers.filter(v => (integrationCategory === "全部" || v.category === integrationCategory) && `${v.name}${v.description}`.toLowerCase().includes(integrationKeyword.toLowerCase()));
  const editS3 = async (row: any) => { try { const detail = await adminAPI.getS3(row.id); setEditingIntegration(detail); const cfg = detail.version.config; settingsForm.setFieldsValue({ name: detail.definition.name, zone: detail.definition.zone, endpoint: `${cfg.use_tls ? "https" : "http"}://${cfg.endpoint}`, region: cfg.region, bucket: cfg.bucket, prefix: cfg.prefix, use_tls: cfg.use_tls, path_style: cfg.path_style }); setDialog("s3"); } catch (e) { message.error((e as Error).message); } };
  const editIntegration = async (row: any) => {
    if (row.type === "S3_STORAGE") return editS3(row);
    try {
      settingsForm.resetFields();
      if (row.type === "LLM") {
        const detail = await aiAdminAPI.get(row.id);
        setEditingIntegration(detail);
        settingsForm.setFieldsValue({ name: detail.name, base_url: detail.config.base_url, model: detail.config.model, monthly_token_budget: detail.config.monthly_token_budget });
        setDialog("ai");
      } else {
        if (row.type === "HTTP_CONNECT_PROXY") {
          const editable = row.status === "PUBLISHED" ? await outboundAPI.reviseProxy(row.id) : row;
          setEditingIntegration({ row: editable });
          settingsForm.setFieldsValue({ name: editable.name, proxy_url: editable.config.proxy_url, username: editable.config.username });
          setDialog("proxy");
          if (row.status === "PUBLISHED") { message.success("已从发布版本创建可编辑修订草稿"); await load(); }
        } else if (row.type === "WECOM_GROUP_BOT") {
          setEditingIntegration({ row });
          settingsForm.setFieldsValue({ name: row.name, proxy_id: row.config.proxy_id }); setDialog("wecom");
        }
      }
    } catch (e) { message.error((e as Error).message); }
  };
  const deleteIntegration = async (row: any) => {
    try {
      if (row.type === "S3_STORAGE" || row.type === "LLM") await adminAPI.deleteIntegration(row.id); else await outboundAPI.delete(row.id);
      message.success(row.status === "PUBLISHED" ? "已发布实例已撤销" : "集成草稿已删除"); await load();
    } catch (e) { message.error((e as Error).message); }
  };
  const publishIntegration = async (row: any) => {
    try {
      if ((row.type === "S3_STORAGE" || row.type === "LLM") && !row.probe_passed) {
        message.warning(row.type === "S3_STORAGE" ? "请先完成连接测试并确保 Bucket 已启用版本控制" : "请先完成模型契约测试");
        return;
      }
      if (row.type === "S3_STORAGE" || row.type === "LLM") await adminAPI.publish(row.id, row.version); else await outboundAPI.publish(row.id, row.version);
      message.success("集成实例已发布"); await load();
    } catch (e) { message.error((e as Error).message); }
  };
  const integrationView = <div className="integration-page">
    <Row gutter={16} className="settings-summary"><Col span={8}><Card><span>集成类型</span><strong>{providers.length}</strong></Card></Col><Col span={8}><Card><span>已配置实例</span><strong>{integrations.length + outbound.length}</strong></Card></Col><Col span={8}><Card><span>已发布</span><strong>{[...integrations, ...outbound].filter(v => v.status === "PUBLISHED").length}</strong></Card></Col></Row>
    <Card className="enterprise-card integration-catalog-card">
      <div className="integration-catalog-header"><div><Typography.Title level={5}>集成目录</Typography.Title><Typography.Text type="secondary">按能力分类管理存储、网络、通知和智能服务</Typography.Text></div><Button icon={<KeyOutlined />} onClick={() => setDialog("secret")}>凭据管理</Button></div>
      <div className="integration-toolbar"><Segmented value={integrationCategory} onChange={v => setIntegrationCategory(String(v))} options={["全部", "存储", "网络", "通知", "智能"]} /><Input allowClear prefix={<SearchOutlined />} placeholder="搜索集成" value={integrationKeyword} onChange={e => setIntegrationKeyword(e.target.value)} /></div>
      <div className="integration-grid">{filteredProviders.map(v => <button key={v.key} className={`integration-provider-card tone-${v.tone}`} disabled={v.disabled} onClick={v.action}><span className="integration-provider-icon">{v.icon}</span><span className="integration-provider-main"><span className="integration-provider-head"><strong>{v.name}</strong><Tag color={v.disabled ? "default" : v.count ? "success" : "blue"}>{v.disabled ? "规划中" : v.count ? `${v.count} 个实例` : "可配置"}</Tag></span><span className="integration-provider-description">{v.description}</span><span className="integration-provider-meta"><Tag>{v.category}</Tag>{v.count > 0 ? "查看并继续配置" : "创建首个实例"}</span></span><PlusOutlined className="integration-provider-action" /></button>)}</div>
    </Card>
    <Card className="enterprise-card" title="集成实例"><Table rowKey={v => `${v.type}-${v.id}`} dataSource={[...integrations, ...outbound]} pagination={false} columns={[{ title: "名称", render: (_, v) => <div className="table-primary"><strong>{v.name}</strong><span>{v.id}</span></div> }, { title: "分类", dataIndex: "type", render: v => <Tag>{integrationTypeText(v)}</Tag> }, { title: "安全域", dataIndex: "zone", render: v => v ? zoneText(v) : "全局" }, { title: "状态", dataIndex: "status", render: (v, row) => <Space size={4}>{status(v)}{row.status === "DRAFT" && (row.type === "S3_STORAGE" || row.type === "LLM") && <Tag color={row.probe_passed ? "success" : "warning"}>{row.probe_passed ? "测试通过" : "待测试"}</Tag>}</Space> }, { title: "版本", dataIndex: "version", render: v => `V${v}` }, { title: "操作", width: 280, render: (_, v) => { const proxyMaintainable = v.type === "HTTP_CONNECT_PROXY" && ["DRAFT", "PUBLISHED"].includes(v.status); return <Space><Button size="small" disabled={v.type !== "S3_STORAGE"} onClick={() => adminAPI.testS3(v.id).then(async () => { message.success("连接测试通过"); await load(); }).catch(e => message.error(e.message))}>测试</Button><Button size="small" icon={<EditOutlined />} disabled={!proxyMaintainable && (v.status !== "DRAFT" || v.type === "SMTP")} onClick={() => editIntegration(v)}>修改</Button><Button size="small" type="link" disabled={v.status !== "DRAFT"} onClick={() => publishIntegration(v)}>发布</Button><Popconfirm title={v.status === "PUBLISHED" ? "确认撤销该代理实例？" : "确认删除该集成草稿？"} description={v.status === "PUBLISHED" ? "仅未被通知实例引用的代理可以撤销。" : "草稿配置及其凭据引用将一并删除，已保存的凭据本身不会删除。"} onConfirm={() => deleteIntegration(v)}><Button size="small" type="text" danger icon={<DeleteOutlined />} disabled={!proxyMaintainable && (v.status !== "DRAFT" || v.type === "SMTP")}>删除</Button></Popconfirm></Space>; } }]}/></Card>
  </div>;
  const route = location.pathname.split("/").pop();
  const pageMeta: Record<string, [string, string]> = {
    organization: ["组织与用户", "管理用户、部门、团队及其业务系统归属"], integrations: ["集成配置", "按能力分类配置外部服务及版本化实例"], transfers: ["交换与传输", "管理双向交换通道和失败任务处置"], operations: ["监控与审计", "配置只读监控令牌并执行独立审计归档"], security: ["凭据与安全", "集中录入加密凭据并管理敏感引用"],
  };
  const keyword = orgKeyword.trim().toLowerCase();
  const match = (...values: any[]) => !keyword || values.some(v => String(v || "").toLowerCase().includes(keyword));
  const orgToolbar = (placeholder: string, label: string, action: () => void, disabled = false) => <div className="managed-toolbar"><Input allowClear prefix={<SearchOutlined />} placeholder={placeholder} value={orgKeyword} onChange={e => setOrgKeyword(e.target.value)} /><Space><Button icon={<ReloadOutlined />} onClick={load}>刷新</Button><Button type="primary" icon={<PlusOutlined />} disabled={disabled} onClick={action}>{label}</Button></Space></div>;
  const editGroup = (group: any) => { setEditingGroup(group); settingsForm.setFieldsValue({ department_id: group.department_id, code: group.code, name: group.name, manager_user_ids: group.manager_user_ids || [] }); setDialog("group"); };
  const editUser = (user: any) => { const membership=user.team_memberships?.[0];const group=groups.find(v=>v.id===membership?.group_id);setSelectedUser(user);settingsForm.setFieldsValue({display_name:user.display_name,avatar_emoji:user.avatar_emoji||"🐼",status:user.status,department_id:group?.department_id,group_id:membership?.group_id,is_manager:!!membership?.is_manager,priority:membership?.priority||0});setDialog("edit-user"); };
  const resetMFA = (user:any) => { setSelectedUser(user); settingsForm.resetFields(); setDialog("mfa-reset"); };
  const reissueActivation = async (user:any) => { try { const value=await adminAPI.reissueActivation(user.id,user.version);setResult(value);message.success("已重新签发激活凭据");load(); } catch(e){message.error((e as Error).message);} };
  const organizationTabs = [
    { key: "departments", label: `部门 ${departments.length}`, children: <>{orgToolbar("搜索部门名称或编码", "新增部门", () => setDialog("department"))}<Table rowKey="id" dataSource={departments.filter(v => match(v.name, v.code, v.status))} pagination={{ pageSize: 10 }} locale={{ emptyText: "暂无部门" }} columns={[{ title: "部门名称", dataIndex: "name", render: (v, r) => <div className="table-primary"><strong>{v}</strong><span>{r.code}</span></div> }, { title: "状态", dataIndex: "status", width: 100, render: status }, { title: "团队", width: 100, render: (_, r) => groups.filter(v => v.department_id === r.id).length }, { title: "业务系统", width: 100, render: (_, r) => systems.filter(v => v.department_id === r.id).length }, { title: "版本", dataIndex: "version", width: 80, render: v => `V${v}` }]} /></> },
    { key: "groups", label: `团队 ${groups.length}`, children: <>{orgToolbar("搜索团队、编码或所属部门", "新增团队", () => { setEditingGroup(undefined); settingsForm.resetFields(); setDialog("group"); }, departments.length === 0)}<Table rowKey="id" dataSource={groups.filter(v => match(v.name, v.code, departments.find(d => d.id === v.department_id)?.name))} pagination={{ pageSize: 10 }} locale={{ emptyText: "暂无团队" }} columns={[{ title: "团队", dataIndex: "name", render: (v, r) => <div className="table-primary"><strong>{v}</strong><span>{r.code}</span></div> }, { title: "所属部门", dataIndex: "department_id", render: v => departments.find(d => d.id === v)?.name || v }, { title: "负责人", dataIndex: "manager_user_ids", render: (ids: string[]) => ids?.map(id => users.find(user => user.id === id)?.display_name || id).join("、") || "—" }, { title: "状态", dataIndex: "status", width: 100, render: status }, { title: "版本", dataIndex: "version", width: 80, render: v => `V${v}` }, { title: "操作", width: 90, render: (_, row) => <Button size="small" icon={<EditOutlined />} onClick={() => editGroup(row)}>修改</Button> }]} /></> },
    { key: "users", label: `用户 ${users.length}`, children: <>{orgToolbar("搜索姓名或登录账号", "新增用户", () => setDialog("user"), groups.length === 0)}<Table rowKey="id" dataSource={users.filter(v => match(v.display_name, v.username, v.status))} pagination={{ pageSize: 10 }} locale={{ emptyText: "暂无用户" }} columns={[{ title: "用户", render: (_, r) => <div className="user-identity"><Avatar className="emoji-avatar">{r.avatar_emoji || "👤"}</Avatar><div className="table-primary"><strong>{r.display_name}</strong><span>{r.username}</span></div></div> }, { title: "用户状态", dataIndex: "status", width: 130, render: status }, { title: "身份验证", dataIndex: "mfa_state", width: 120, render: v => <Tag color={v === "ACTIVE" ? "success" : "warning"}>{v === "ACTIVE" ? "已绑定" : "待绑定"}</Tag> }, { title: "版本", dataIndex: "version", width: 80, render: v => `V${v}` }, {title:"操作",width:260,render:(_,row)=><Space><Button size="small" icon={<EditOutlined/>} disabled={!['ACTIVE','DISABLED'].includes(row.status)} onClick={()=>editUser(row)}>修改</Button><Button size="small" disabled={row.status!=="PENDING_ACTIVATION"} onClick={()=>reissueActivation(row)}>重发激活</Button><Button size="small" danger disabled={row.status!=="ACTIVE"} onClick={()=>resetMFA(row)}>重置验证器</Button></Space>}]} /><div className="settings-action-row"><div><strong>验证器重置复核</strong><span>由另一位安全管理员输入恢复申请 ID 完成独立复核。</span></div><Button onClick={()=>{settingsForm.resetFields();setDialog("mfa-approve")}}>审批重置</Button></div></> },
    { key: "systems", label: `业务系统 ${systems.length}`, children: <>{orgToolbar("搜索业务系统、编码或责任部门", "新增业务系统", () => setDialog("system"), departments.length === 0)}<Table rowKey="id" dataSource={systems.filter(v => match(v.name, v.code, departments.find(d => d.id === v.department_id)?.name))} pagination={{ pageSize: 10 }} locale={{ emptyText: "暂无业务系统" }} columns={[{ title: "业务系统", dataIndex: "name", render: (v, r) => <div className="table-primary"><strong>{v}</strong><span>{r.code}</span></div> }, { title: "责任部门", dataIndex: "department_id", render: v => departments.find(d => d.id === v)?.name || "—" }, { title: "允许方向", dataIndex: "allowed_directions", render: (values: string[]) => <Space size={4} wrap>{values?.map(v => <Tag key={v}>{directionText(v)}</Tag>)}</Space> }, { title: "状态", dataIndex: "status", width: 100, render: status }]} /></> },
  ];
  const organizationView = <div className="settings-section-stack"><Row gutter={16} className="settings-summary"><Col span={6}><Card><span>部门</span><strong>{departments.length}</strong></Card></Col><Col span={6}><Card><span>团队</span><strong>{groups.length}</strong></Card></Col><Col span={6}><Card><span>用户</span><strong>{users.length}</strong></Card></Col><Col span={6}><Card><span>业务系统</span><strong>{systems.length}</strong></Card></Col></Row><Card className="enterprise-card organization-console"><Tabs defaultActiveKey="departments" onChange={() => setOrgKeyword("")} items={organizationTabs} /></Card></div>;
  const operationsView = <div className="settings-section-stack"><Row gutter={16} className="settings-summary"><Col span={8}><Card><span>监控状态</span><strong className="summary-status">{monitoring.enabled ? "运行中" : "未配置"}</strong></Card></Col><Col span={8}><Card><span>有效令牌</span><strong>{monitoringTokens.filter(v => v.enabled).length}</strong></Card></Col><Col span={8}><Card><span>归档批次</span><strong>{archives.length}</strong></Card></Col></Row><Card className="enterprise-card settings-tab-card"><Tabs items={[{ key: "monitoring", label: "监控接入", children: tabs.find(v => v.key === "monitoring")?.children }, { key: "archives", label: "审计归档", children: tabs.find(v => v.key === "archives")?.children }]} /></Card></div>;
  const credentialTypes = [{ title: "存储凭据", description: "S3 Access Key 与 Secret Key", icon: <DatabaseOutlined />, tags: ["S3_ACCESS_KEY", "S3_SECRET_KEY"] }, { title: "通知凭据", description: "企业微信 Webhook、代理及 SMTP 密码", icon: <MailOutlined />, tags: ["WECOM_WEBHOOK", "PROXY_PASSWORD", "SMTP_PASSWORD"] }, { title: "安全与智能", description: "审计签名密钥与模型 API Key", icon: <SafetyCertificateOutlined />, tags: ["AUDIT_SIGNING_KEY", "LLM_API_KEY"] }];
  const securityView = <div className="settings-section-stack"><Alert showIcon type="warning" message="敏感值写入后不可读取" description="凭据由主密钥加密保存，业务配置仅引用凭据 ID；轮换时请创建新凭据并发布新的配置版本。" /><div className="credential-grid">{credentialTypes.map(v => <Card key={v.title} className="credential-card"><div className="credential-card-icon">{v.icon}</div><div className="credential-card-main"><strong>{v.title}</strong><span>{v.description}</span><div>{v.tags.map(tag => <Tag key={tag}>{tag}</Tag>)}</div></div></Card>)}</div><Card className="enterprise-card" title="凭据操作" extra={<Tag color="blue">仅写入</Tag>}><div className="settings-action-row"><div><strong>录入新的加密凭据</strong><span>保存成功后仅返回引用 ID，原始值不会再次展示。</span></div><Button type="primary" icon={<KeyOutlined />} onClick={() => setDialog("secret")}>录入凭据</Button></div></Card><Card className="enterprise-card" title="安全约束"><div className="security-rules"><span><TeamOutlined /> 仅系统管理员可录入或引用凭据</span><span><SafetyCertificateOutlined /> 凭据内容不会写入操作日志和接口响应</span><span><CloudServerOutlined /> 配置发布后固定引用版本，避免静默变更</span></div></Card></div>;
  const content = route === "organization" ? organizationView : route === "integrations" ? integrationView : route === "transfers" ? tabs.find(v => v.key === "transfers")?.children : route === "operations" ? operationsView : securityView;
  return (
    <div className="settings-page">
      <div className="page-heading">
        <div>
          <Typography.Title level={3}>{pageMeta[route || "organization"]?.[0] || "系统设置"}</Typography.Title>
          <Typography.Text type="secondary">
            {pageMeta[route || "organization"]?.[1]}
          </Typography.Text>
        </div>
      </div>
      {content}
      <Modal
        open={!!dialog}
        title={
          {
            department: "新建部门",
            group: editingGroup ? "修改团队" : "新建团队",
            user: "创建待激活用户",
            "edit-user":"修改用户",
            "mfa-reset":"发起验证器重置",
            "mfa-approve":"审批验证器重置",
            system: "新建业务系统",
            secret: "录入秘密",
            s3: editingIntegration ? "修改 S3 存储草稿" : "新建 S3 存储草稿",
            "monitoring-token": "签发 Zabbix 只读令牌",
            proxy:editingIntegration ? "修改 HTTP CONNECT Proxy 草稿" : "新建 HTTP CONNECT Proxy",
            wecom:editingIntegration ? "修改企业微信群机器人草稿" : "新建企业微信群机器人",
            archive:"执行审计日归档",
            ai:editingIntegration ? "修改 AI 文字辅助草稿" : "新建 AI 文字辅助集成",
          }[dialog || ""]
        }
        footer={null}
        onCancel={() => { setDialog(undefined); setEditingIntegration(undefined); setEditingGroup(undefined); setSelectedUser(undefined); settingsForm.resetFields(); }}
        destroyOnClose
        width={["s3", "user", "edit-user", "department", "group", "system"].includes(dialog || "") ? 760 : 560}
      >
        <Form form={settingsForm} layout="vertical" className="enterprise-form" onFinish={submit}>
          {dialog === "department" && (
            <>
              <div className="form-section-title user-form-divider"><strong>部门信息</strong><span>部门编码用于权限范围和流程绑定，创建后应保持稳定</span></div>
              <Form.Item
                name="code"
                label="部门编码"
                extra="2–64 位大写字母、数字、下划线或连字符"
                rules={[{ required: true }, { pattern: /^[A-Z0-9][A-Z0-9_-]{1,63}$/, message: "请输入合规的部门编码" }]}
              >
                <Input placeholder="例如 INFORMATION_SECURITY" />
              </Form.Item>
              <Form.Item
                name="name"
                label="部门名称"
                rules={[{ required: true }]}
              >
                <Input placeholder="例如 信息安全部" maxLength={128} />
              </Form.Item>
              <div className="form-section-title user-form-divider"><strong>责任人配置</strong><span>所选人员将获得该部门范围的“部门负责人”角色</span></div>
              <Form.Item className="form-item-full" name="manager_user_ids" label="部门负责人" extra="可多选，也可以创建部门后再进行授权"><Select allowClear mode="multiple" showSearch optionFilterProp="label" placeholder="请选择现有用户" options={users.filter(v => v.status !== "DISABLED").map(v => ({ value: v.id, label: `${v.display_name}（${v.username}）` }))} /></Form.Item>
              <Alert className="user-form-note" type="info" showIcon message="部门是组织权限的一级边界" description="团队、业务系统和部门级审批策略都将引用该部门；负责人权限仅在本部门范围内生效。" />
            </>
          )}
          {dialog === "group" && (
            <>
              <div className="form-section-title user-form-divider"><strong>团队信息</strong><span>团队是申请发起、审批责任和成员归属的基本单位</span></div>
              <Form.Item
                name="department_id"
                label="所属部门"
                rules={[{ required: true }]}
              >
                <Select placeholder="请选择所属部门"
                  options={departments.map((v) => ({
                    value: v.id,
                    label: v.name,
                  }))}
                />
              </Form.Item>
              <Form.Item
                name="code"
                label="团队编码"
                extra={editingGroup ? "团队编码创建后不可修改" : "创建后保持稳定，用于接口与审计标识"}
                rules={[{ required: true }, { pattern: /^[A-Z0-9][A-Z0-9_-]{1,63}$/, message: "请输入合规的团队编码" }]}
              >
                <Input disabled={!!editingGroup} placeholder="例如 DBA_PLATFORM" />
              </Form.Item>
              <Form.Item
                name="name"
                label="团队名称"
                rules={[{ required: true }]}
              >
                <Input placeholder="例如 数据库平台团队" maxLength={128} />
              </Form.Item>
              <div className="form-section-title user-form-divider"><strong>{editingGroup ? "团队负责人" : "初始负责人"}</strong><span>负责人将成为团队成员，并参与团队负责人审批节点解析</span></div>
              <Form.Item className="form-item-full" name="manager_user_ids" label="团队负责人" rules={[{ required: true, message: "请至少选择一位负责人" }]}><Select mode="multiple" showSearch optionFilterProp="label" placeholder="请选择一位或多位负责人" options={users.filter(v => v.status !== "DISABLED").map(v => ({ value: v.id, label: `${v.display_name}（${v.username}）` }))} /></Form.Item>
              <Alert className="user-form-note" type="info" showIcon message="选择顺序决定审批优先级" description="第一位负责人的优先级最高；后续可通过成员管理调整负责人和优先级。" />
            </>
          )}
          {dialog === "user" && (
            <>
              <Form.Item
                name="username"
                label="登录账号"
                extra="3–64 位小写字母、数字、点、下划线或连字符"
                rules={[{ required: true }, { pattern: /^[a-z0-9._-]{3,64}$/, message: "请输入合规的登录账号" }]}
              >
                <Input placeholder="例如 zhang.san" autoComplete="off" />
              </Form.Item>
              <Form.Item
                name="display_name"
                label="显示名"
                rules={[{ required: true }]}
              >
                <Input placeholder="例如 张三" />
              </Form.Item>
              <Form.Item name="avatar_emoji" label="动物头像" initialValue="🐼" rules={[{ required: true }]}><Select className="emoji-select" options={avatarEmojiOptions} /></Form.Item>
              <div className="form-section-title user-form-divider"><strong>组织归属</strong><span>确定用户所属团队及团队内责任</span></div>
              <Form.Item name="department_id" label="所属部门" rules={[{ required: true }]}><Select placeholder="请选择部门" options={departments.map(v => ({ value: v.id, label: `${v.name}（${v.code}）` }))} /></Form.Item>
              <Form.Item name="group_id" label="所属团队" rules={[{ required: true }]}><Select placeholder={userDepartment ? "请选择团队" : "请先选择部门"} disabled={!userDepartment} options={groups.filter(v => v.department_id === userDepartment).map(v => ({ value: v.id, label: `${v.name}（${v.code}）` }))} /></Form.Item>
              <Form.Item name="is_manager" label="团队内身份" initialValue={false}><Select options={[{ value: false, label: "普通成员" }, { value: true, label: "团队负责人" }]} /></Form.Item>
              <Form.Item name="priority" label="审批优先级" initialValue={0} extra="数值越小越优先"><Input type="number" min={0} max={65535} /></Form.Item>
              <div className="form-section-title user-form-divider"><strong>角色与授权</strong><span>角色决定功能权限，范围限制权限生效边界</span></div>
              <Form.Item name="role_ids" label="系统角色" rules={[{ required: true }]}><Select mode="multiple" placeholder="请选择一个或多个角色" options={roles.map(v => ({ value: v.id, label: `${v.name}（${v.code}）` }))} /></Form.Item>
              <Form.Item name="scope_type" label="授权范围" initialValue="GLOBAL" rules={[{ required: true }]}><Select options={[{ value: "GLOBAL", label: "全局" }, { value: "DEPARTMENT", label: "所属部门" }, { value: "GROUP", label: "所属团队" }]} /></Form.Item>
              <Alert className="user-form-note" type="info" showIcon message={userScope === "GLOBAL" ? "所选角色将在全局范围生效" : userScope === "DEPARTMENT" ? "所选角色仅在该用户所属部门生效" : "所选角色仅在该用户所属团队生效"} description="用户创建后处于待激活状态，系统会生成一份 24 小时有效的一次性激活凭据。" />
            </>
          )}
          {dialog === "edit-user" && <><Alert className="user-form-note" type="info" showIcon message={`登录账号 ${selectedUser?.username} 不可修改`} description="停用用户会立即撤销其全部登录会话；团队变更会同步影响申请范围和团队负责人审批。"/><div className="form-section"><div className="form-section-title"><strong>基本信息</strong><span>维护用户显示身份、头像及访问状态</span></div><Row gutter={16}><Col span={10}><Form.Item name="display_name" label="显示名" rules={[{required:true}]}><Input maxLength={128} placeholder="请输入用户显示名"/></Form.Item></Col><Col span={7}><Form.Item name="avatar_emoji" label="人员头像" rules={[{required:true}]}><Select className="emoji-select" options={avatarEmojiOptions}/></Form.Item></Col><Col span={7}><Form.Item name="status" label="用户状态" rules={[{required:true}]}><Select options={[{value:"ACTIVE",label:"启用"},{value:"DISABLED",label:"停用"}]}/></Form.Item></Col></Row></div><div className="form-section"><div className="form-section-title"><strong>组织归属</strong><span>部门决定可选团队范围，切换部门后需要重新选择团队</span></div><Row gutter={16}><Col span={12}><Form.Item name="department_id" label="所属部门" rules={[{required:true}]}><Select showSearch optionFilterProp="label" onChange={()=>settingsForm.setFieldValue('group_id',undefined)} options={departments.map(v=>({value:v.id,label:`${v.name}（${v.code}）`}))}/></Form.Item></Col><Col span={12}><Form.Item name="group_id" label="所属团队" rules={[{required:true}]}><Select showSearch optionFilterProp="label" placeholder={userDepartment?'请选择团队':'请先选择部门'} disabled={!userDepartment} options={groups.filter(v=>v.department_id===userDepartment).map(v=>({value:v.id,label:`${v.name}（${v.code}）`}))}/></Form.Item></Col></Row></div><div className="form-section"><div className="form-section-title"><strong>团队责任</strong><span>团队负责人参与审批候选人解析，优先级数值越小越优先</span></div><Row gutter={16}><Col span={12}><Form.Item name="is_manager" label="团队内身份" rules={[{required:true}]}><Select options={[{value:false,label:'普通成员'},{value:true,label:'团队负责人'}]}/></Form.Item></Col><Col span={12}><Form.Item name="priority" label="审批优先级"><Input type="number" min={0} max={65535} placeholder="0"/></Form.Item></Col></Row></div></>}
          {dialog === "mfa-reset" && <><Alert type="warning" showIcon message="该操作需要另一位安全管理员复核" description="复核通过后旧验证器和全部会话立即失效，并生成仅 30 分钟有效的重绑凭据。"/><Form.Item name="note" label="线下身份核验记录" rules={[{required:true,min:10,max:1000}]}><Input.TextArea rows={4} placeholder="请记录工牌、负责人或其他线下身份核验过程"/></Form.Item></>}
          {dialog === "mfa-approve" && <><Alert type="warning" showIcon message="发起人和目标用户不能审批本次重置" description="审批要求当前安全管理员在最近 5 分钟内完成过 MFA 验证。"/><Form.Item name="recovery_id" label="恢复申请 ID" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="note" label="独立复核记录" rules={[{required:true,min:10,max:1000}]}><Input.TextArea rows={4}/></Form.Item></>}
          {dialog === "system" && (
            <>
              <div className="form-section-title user-form-divider"><strong>系统标识</strong><span>用于申请选择、审计检索和流程策略范围绑定</span></div>
              <Form.Item
                name="code"
                label="系统编码"
                rules={[{ required: true }, { pattern: /^[A-Z0-9][A-Z0-9_-]{1,63}$/, message: "请输入合规的系统编码" }]}
              >
                <Input placeholder="例如 ERP_PRODUCTION" />
              </Form.Item>
              <Form.Item
                name="name"
                label="系统名称"
                rules={[{ required: true }]}
              >
                <Input placeholder="例如 ERP 生产系统" maxLength={128} />
              </Form.Item>
              <div className="form-section-title user-form-divider"><strong>归属与交换策略</strong><span>明确系统责任边界以及允许发起的文件交换方向</span></div>
              <Form.Item name="department_id" label="责任部门" rules={[{ required: true, message: "请选择责任部门" }]}>
                <Select
                  placeholder="请选择责任部门"
                  options={departments.map((v) => ({
                    value: v.id,
                    label: v.name,
                  }))}
                />
              </Form.Item>
              <Form.Item
                name="allowed_directions"
                label="允许方向"
                initialValue={["PROD_TO_OFFICE", "OFFICE_TO_PROD"]}
                rules={[{ required: true }]}
              >
                <Select
                  mode="multiple"
                  options={[
                    { value: "PROD_TO_OFFICE", label: "生产到办公" },
                    { value: "OFFICE_TO_PROD", label: "办公到生产" },
                  ]}
                />
              </Form.Item>
              <Alert className="user-form-note" type="warning" showIcon message="方向策略会限制申请入口" description="未勾选的方向无法为该业务系统发起申请；收紧范围前请确认现有流程和业务需求。" />
            </>
          )}
          {dialog === "secret" && (
            <>
              <Form.Item
                name="name"
                label="秘密名称"
                rules={[{ required: true }]}
              >
                <Input />
              </Form.Item>
              <Form.Item
                name="purpose"
                label="用途"
                rules={[{ required: true }]}
              >
                <Select
                  options={[
                    { value: "S3_ACCESS_KEY", label: "S3 Access Key" },
                    { value: "S3_SECRET_KEY", label: "S3 Secret Key" },
                    { value: "WECOM_WEBHOOK", label: "企业微信 Webhook" },
                    { value: "PROXY_PASSWORD", label: "Proxy 密码" },
                    { value: "SMTP_PASSWORD", label: "SMTP 密码" },
                    { value: "AUDIT_SIGNING_KEY", label: "审计签名密钥" },
                    { value: "LLM_API_KEY", label: "LLM API Key" },
                  ]}
                />
              </Form.Item>
              <Form.Item
                name="value"
                label="秘密值"
                rules={[{ required: true }]}
              >
                <Input.Password autoComplete="new-password" />
              </Form.Item>
            </>
          )}
          {dialog === "s3" && (
            <>
              <Alert className="user-form-note" type="info" showIcon message="Bucket 必须启用版本控制" description="云渡使用不可变 Version ID 冻结审批文件。请先在 MinIO/S3 管理端启用 Versioning，否则连接测试不会通过。" />
              <Form.Item name="name" label="名称" rules={[{ required: true }]}>
                <Input />
              </Form.Item>
              <Form.Item
                name="zone"
                label="安全域"
                rules={[{ required: true }]}
              >
                <Select
                  options={[
                    { value: "OFFICE", label: "办公" },
                    { value: "PRODUCTION", label: "生产" },
                  ]}
                />
              </Form.Item>
              <Form.Item
                name="endpoint"
                label="访问地址"
                rules={[{ required: true }]}
              >
                <Input placeholder="http://10.0.0.10:9000 或 s3.example.com" />
              </Form.Item>
              <Form.Item name="region" label="Region">
                <Input />
              </Form.Item>
              <Form.Item
                name="bucket"
                label="Bucket"
                rules={[{ required: true }]}
              >
                <Input />
              </Form.Item>
              <Form.Item name="prefix" label="允许前缀">
                <Input />
              </Form.Item>
              <Form.Item
                name="access_key"
                label="Access Key"
                extra={editingIntegration ? "留空则继续使用原凭据" : undefined}
                rules={editingIntegration ? [] : [{ required: true }]}
              >
                <Input.Password autoComplete="new-password" placeholder="请输入对象存储 Access Key" />
              </Form.Item>
              <Form.Item
                name="secret_key"
                label="Secret Key"
                extra={editingIntegration ? "留空则继续使用原凭据" : undefined}
                rules={editingIntegration ? [] : [{ required: true }]}
              >
                <Input.Password autoComplete="new-password" placeholder="请输入对象存储 Secret Key" />
              </Form.Item>
              <Form.Item name="use_tls" label="TLS" initialValue={true}>
                <Select
                  options={[
                    { value: true, label: "启用" },
                    { value: false, label: "关闭（仅开发）" },
                  ]}
                />
              </Form.Item>
              <Form.Item name="path_style" label="寻址方式" initialValue={true}>
                <Select
                  options={[
                    { value: true, label: "Path Style" },
                    { value: false, label: "Virtual Hosted" },
                  ]}
                />
              </Form.Item>
            </>
          )}
          {dialog === "monitoring-token"&&<><Form.Item name="name" label="令牌名称" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="allowed_cidrs" label="允许采集源 CIDR（每行一个）" rules={[{required:true}]}><Input.TextArea rows={4} placeholder="10.10.20.15/32"/></Form.Item><Typography.Paragraph type="secondary">令牌至少 256 bit，只在创建后显示一次；每个来源最多 12 次/分钟。</Typography.Paragraph></>}
          {dialog==='proxy'&&<><Form.Item name="name" label="名称" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="proxy_url" label="Proxy URL" rules={[{required:true}]}><Input placeholder="http://proxy.internal:3128"/></Form.Item><Form.Item name="username" label="Basic 用户名"><Input/></Form.Item><Form.Item name="password" label="Proxy 密码" extra={editingIntegration ? "留空则继续使用原凭据；输入新值将完成凭据轮换" : "代理无需认证时可留空"}><Input.Password autoComplete="new-password"/></Form.Item><Typography.Paragraph type="secondary">目的地固定为 qyapi.weixin.qq.com:443，TLS 校验不可关闭。</Typography.Paragraph></>}
          {dialog==='wecom'&&<><Form.Item name="name" label="群别名" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="proxy_id" label="已发布 Proxy" rules={[{required:true}]}><Select options={outbound.filter(v=>v.type==='HTTP_CONNECT_PROXY'&&v.status==='PUBLISHED').map(v=>({value:v.id,label:v.name}))}/></Form.Item><Form.Item name="webhook" label="Webhook 地址" extra={editingIntegration ? "留空则继续使用原 Webhook；输入新值将完成凭据轮换" : "完整地址会加密保存，后续不再回显"} rules={editingIntegration ? [] : [{required:true}]}><Input.Password autoComplete="new-password" placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..."/></Form.Item></>}
          {dialog==='archive'&&<><Form.Item name="range" label="UTC 日归档时间窗" rules={[{required:true}]}><DatePicker.RangePicker showTime format="YYYY-MM-DD HH:mm:ss"/></Form.Item><Form.Item name="storage_version_id" label="独立审计 S3 版本 ID" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="signing_key_secret_id" label="审计签名秘密 ID" rules={[{required:true}]}><Input/></Form.Item></>}
          {dialog==='ai'&&<><Form.Item name="name" label="名称" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="base_url" label="兼容 Chat Completions 的 HTTPS Base URL" rules={[{required:true}]}><Input placeholder="https://model.example/v1"/></Form.Item><Form.Item name="model" label="模型" rules={[{required:true}]}><Input/></Form.Item><Form.Item name="api_key" label="LLM API Key" extra={editingIntegration ? "留空则继续使用原 API Key；输入新值将完成凭据轮换" : "密钥会加密保存，后续不再回显"} rules={editingIntegration ? [] : [{required:true}]}><Input.Password autoComplete="new-password"/></Form.Item><Form.Item name="monthly_token_budget" label="月度计费 Token 上限" rules={[{required:true}]}><Input type="number" min={1000}/></Form.Item></>}
          <Button block type="primary" htmlType="submit" loading={submitting} disabled={submitting}>
            {dialog === "user" ? "创建用户并生成激活凭据" : "保存"}
          </Button>
        </Form>
      </Modal>
      <Modal
        open={!!result}
        title="一次性凭据/引用"
        onOk={() => setResult(undefined)}
        onCancel={() => setResult(undefined)}
        cancelButtonProps={{ style: { display: "none" } }}
      >
        <Typography.Paragraph type="warning">
          以下内容仅显示一次，请通过企业已验证渠道安全传递或保存引用。
        </Typography.Paragraph>
        <pre className="one-time-result">{JSON.stringify(result, null, 2)}</pre>
      </Modal>
    </div>
  );
}
