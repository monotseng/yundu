import {
  GlobalOutlined,
  LockOutlined,
  LoginOutlined,
  SafetyCertificateOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Button, Form, Input, Modal, Typography, message } from "antd";
import Brand from "@/components/Brand";
import { useI18n } from "@/i18n";
import { useState } from "react";

type PasswordValues = { username: string; password: string };

export default function Login() {
  const { language, toggleLanguage } = useI18n();
  const [challenge, setChallenge] = useState("");
  const [pendingUser, setPendingUser] = useState("");
  const [loading, setLoading] = useState(false);
  const [totpForm] = Form.useForm();
  const post = async (path: string, body: unknown) => {
    const response = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(body),
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.detail || "登录失败");
    return data;
  };
  const password = async (values: PasswordValues) => {
    setLoading(true);
    try {
      const data = await post("/api/v1/auth/password", values);
      setPendingUser(values.username);
      setChallenge(data.challenge);
      totpForm.resetFields();
    } catch (error) {
      message.error((error as Error).message);
    } finally {
      setLoading(false);
    }
  };
  const totp = async (values: { code: string }) => {
    setLoading(true);
    try {
      await post("/api/v1/auth/totp", { challenge, code: values.code });
      location.hash = "#/dashboard";
    } catch (error) {
      message.error((error as Error).message);
    } finally {
      setLoading(false);
    }
  };
  const closeVerification = () => {
    setChallenge("");
    setPendingUser("");
    totpForm.resetFields();
  };
  return (
    <div className="login-page">
      <Button
        className="public-language-switch"
        icon={<GlobalOutlined />}
        onClick={toggleLanguage}
        title={language === "zh-CN" ? "Switch to English" : "切换为中文"}
      >
        {language === "zh-CN" ? "EN" : "中文"}
      </Button>
      <section className="login-brand">
        <Brand />
        <div>
          <Typography.Title>
            让文件有序流转，
            <br />
            让协作安全、高效、可追溯。
          </Typography.Title>
          <Typography.Paragraph>
            面向企业多区域协作的文件交换与审批平台
          </Typography.Paragraph>
        </div>
        <div className="login-points">
          <span>双向安全交换</span>
          <span>审批策略管控</span>
          <span>全链路审计</span>
        </div>
      </section>
      <main className="login-panel">
        <div className="login-box">
          <Typography.Title level={2}>登录云渡</Typography.Title>
          <Typography.Text type="secondary">
            使用您的企业账号进入文件交换工作台
          </Typography.Text>
          <Form className="login-form" layout="vertical" onFinish={password}>
            <Form.Item
              label="账号"
              name="username"
              rules={[{ required: true, message: "请输入账号" }]}
            >
              <Input
                size="large"
                prefix={<UserOutlined />}
                autoComplete="username"
                placeholder="请输入账号"
              />
            </Form.Item>
            <Form.Item
              label="密码"
              name="password"
              rules={[{ required: true, message: "请输入密码" }]}
            >
              <Input.Password
                size="large"
                prefix={<LockOutlined />}
                autoComplete="current-password"
                placeholder="请输入密码"
              />
            </Form.Item>
            <Button
              block
              type="primary"
              htmlType="submit"
              loading={loading && !challenge}
              size="large"
              icon={<LoginOutlined />}
            >
              登录
            </Button>
          </Form>
          <p className="login-help">
            仅限已授权用户访问 · 登录行为将被安全审计
          </p>
        </div>
      </main>
      <Modal
        open={!!challenge}
        footer={null}
        onCancel={closeVerification}
        width={420}
        centered
        destroyOnClose
        title={
          <span className="verification-title">
            <SafetyCertificateOutlined /> 身份验证
          </span>
        }
      >
        <Typography.Paragraph
          type="secondary"
          className="verification-description"
        >
          账号 <Typography.Text strong>{pendingUser}</Typography.Text>{" "}
          验证成功，请输入身份验证器中的动态验证码。
        </Typography.Paragraph>
        <Form form={totpForm} layout="vertical" onFinish={totp}>
          <Form.Item
            label="动态验证码"
            name="code"
            rules={[
              {
                required: true,
                pattern: /^\d{6}$/,
                message: "请输入六位数字验证码",
              },
            ]}
          >
            <Input
              className="verification-code"
              size="large"
              autoFocus
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              placeholder="请输入 6 位验证码"
            />
          </Form.Item>
          <Button
            block
            type="primary"
            htmlType="submit"
            loading={loading}
            size="large"
          >
            确认并进入系统
          </Button>
        </Form>
      </Modal>
    </div>
  );
}
