import { Alert, Card, Empty, Typography } from 'antd';
import { useLocation } from '@umijs/renderer-react';
const names:Record<string,string>={'/requests':'我的申请','/requests/new':'新建交换','/approvals':'我的审批','/notifications':'通知中心','/audit':'审计中心','/settings':'系统设置'};
export default function ComingSoon(){const {pathname}=useLocation();const name=names[pathname]||'功能页面';return <div><div className="page-heading"><div><Typography.Title level={3}>{name}</Typography.Title><Typography.Text type="secondary">云渡文件交换平台</Typography.Text></div></div><Card><Alert type="info" showIcon message={`${name}将在对应开发阶段接入真实 API`} description="当前页面明确展示实施状态，不使用静态数据或假成功响应。"/><Empty description="功能尚未启用"/></Card></div>}
