import {
  AuditOutlined,
  BellOutlined,
  CheckSquareOutlined,
  DownloadOutlined,
  FileAddOutlined,
  HomeOutlined,
  SettingOutlined,
  SwapOutlined,
  ApartmentOutlined,
  ApiOutlined,
  CloudServerOutlined,
  ControlOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Link, Outlet, useLocation, useNavigate } from "@umijs/renderer-react";
import { Avatar, Layout as AntLayout, Menu, Space, Tag } from "antd";
import Brand from "@/components/Brand";
import { useEffect, useState } from "react";
import { getRuntime, type Runtime } from "@/api/runtime";

const { Header, Sider, Content } = AntLayout;
const navItems: any[] = [
  {
    key: "/dashboard",
    icon: <HomeOutlined />,
    label: <Link to="/dashboard">首页概览</Link>,
  },
  {
    key: "/requests",
    icon: <SwapOutlined />,
    label: <Link to="/requests">我的申请</Link>,
    permissions: ["request.read.own"],
  },
  {
    key: "/requests/new",
    icon: <FileAddOutlined />,
    label: <Link to="/requests/new">新建交换</Link>,
    permissions: ["request.create"],
  },
  {
    key: "/downloads",
    icon: <DownloadOutlined />,
    label: <Link to="/downloads">文件领取</Link>,
    permissions: ["download.own"],
  },
  {
    key: "/approvals",
    icon: <CheckSquareOutlined />,
    label: <Link to="/approvals">我的审批</Link>,
    permissions: ["approval.act", "approval.group", "approval.department", "approval.security"],
  },
  {
    key: "/notifications",
    icon: <BellOutlined />,
    label: <Link to="/notifications">通知中心</Link>,
  },
  {
    key: "/audit",
    icon: <AuditOutlined />,
    label: <Link to="/audit">审计中心</Link>,
    permissions: ["audit.read"],
  },
  {
    key: "settings-root",
    icon: <SettingOutlined />,
    label: "系统设置",
    children: [
      { key: "/settings/organization", icon: <ApartmentOutlined />, label: <Link to="/settings/organization">组织与用户</Link>, permissions:["organization.manage","user.manage"] },
      { key: "/settings/workflows", icon: <CheckSquareOutlined />, label: <Link to="/settings/workflows">审批流程</Link>, permissions:["workflow.manage","workflow.publish"] },
      { key: "/settings/integrations", icon: <ApiOutlined />, label: <Link to="/settings/integrations">集成配置</Link>, permissions:["integration.read","integration.manage"] },
      { key: "/settings/transfers", icon: <SwapOutlined />, label: <Link to="/settings/transfers">交换与传输</Link>, permissions:["operations.read","operations.manage"] },
      { key: "/settings/operations", icon: <CloudServerOutlined />, label: <Link to="/settings/operations">监控与审计</Link>, permissions:["monitoring.manage","audit.read"] },
      { key: "/settings/security", icon: <ControlOutlined />, label: <Link to="/settings/security">凭据与安全</Link>, permissions:["secret.manage","security.manage"] },
    ],
  },
];
export default function AppLayout() {
  const location = useLocation();
  const navigate = useNavigate();
  const [runtime, setRuntime] = useState<Runtime>();
  const [user, setUser] = useState<{ display_name: string; avatar_emoji?: string; permissions: string[] }>();
  const [checking, setChecking] = useState(true);
  useEffect(() => {
    getRuntime()
      .then(setRuntime)
      .catch(() => undefined);
    fetch("/api/v1/auth/me", { credentials: "same-origin" })
      .then(async (response) => {
        if (!response.ok) throw new Error();
        setUser(await response.json());
      })
      .catch(() => navigate("/login", { replace: true }))
      .finally(() => setChecking(false));
  }, []);
  const logout = async () => {
    await fetch("/api/v1/auth/logout", {
      method: "POST",
      credentials: "same-origin",
    });
    navigate("/login", { replace: true });
  };
  const zone = runtime?.portal_zone;
  const permissions = new Set(user?.permissions || []);
  const allowed = (entry:any) => !entry.permissions?.length || entry.permissions.some((value:string)=>permissions.has(value));
  const items = navItems.map(entry => entry.children ? { ...entry, children: entry.children.filter(allowed) } : entry).filter(entry => entry.children ? entry.children.length > 0 : allowed(entry)).map(({permissions:_,...entry}) => entry.children ? {...entry,children:entry.children.map(({permissions:__,...child}:any)=>child)} : entry);
  const permittedPaths = new Set<string>(items.flatMap((entry:any)=>entry.children ? entry.children.map((child:any)=>child.key) : [entry.key]));
  useEffect(() => { if (!checking && user && location.pathname !== "/dashboard" && !permittedPaths.has(location.pathname)) navigate("/dashboard", { replace:true }); }, [checking,user,location.pathname]);
  const zoneText =
    zone === "OFFICE"
      ? "办公网络入口"
      : zone === "PRODUCTION"
        ? "生产网络入口"
        : "入口安全域未识别";
  if (checking) return <div className="session-loading">正在验证会话…</div>;
  return (
    <AntLayout className="app-shell">
      <Sider width={232} theme="light">
        <div className="sider-brand">
          <Brand />
        </div>
        <Menu mode="inline" selectedKeys={[location.pathname]} defaultOpenKeys={location.pathname.startsWith("/settings") ? ["settings-root"] : []} items={items} />
        <div className="sider-foot">
          纯 Web 安全交换
          <br />
          <small>版本 {runtime?.version || "dev"}</small>
        </div>
      </Sider>
      <AntLayout>
        <Header className="topbar">
          <Space>
            <Tag color={zone === "UNKNOWN" ? "warning" : "blue"}>
              {zoneText}
            </Tag>
            <span className="top-hint">当前入口决定允许的上传与领取方向</span>
          </Space>
          <Space className="user-action" onClick={logout}>
            <Avatar className="emoji-avatar" icon={user?.avatar_emoji ? undefined : <UserOutlined />}>{user?.avatar_emoji}</Avatar>
            <span>{user?.display_name}</span>
            <span className="logout-text">退出</span>
          </Space>
        </Header>
        <Content className="content">
          <Outlet />
        </Content>
      </AntLayout>
    </AntLayout>
  );
}
