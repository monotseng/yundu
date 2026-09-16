import { defineConfig } from "@umijs/max";

export default defineConfig({
  title: "云渡文件交换平台",
  antd: {},
  history: { type: "hash" },
  npmClient: "npm",
  links: [{ rel: "icon", type: "image/svg+xml", href: "/favicon.svg" }],
  routes: [
    { path: "/login", component: "./Login" },
    { path: "/activate", component: "./Activate" },
    {
      path: "/",
      component: "./Layout",
      routes: [
        { path: "/", redirect: "/dashboard" },
        { path: "/dashboard", component: "./Dashboard" },
        { path: "/requests", component: "./Requests" },
        { path: "/requests/new", component: "./NewRequest" },
        { path: "/downloads", component: "./Downloads" },
        { path: "/approvals", component: "./Approvals" },
        { path: "/workflows", redirect: "/settings/workflows" },
        { path: "/notifications", component: "./Notifications" },
        { path: "/audit", component: "./Audit" },
        { path: "/settings", redirect: "/settings/organization" },
        { path: "/settings/organization", component: "./Settings" },
        { path: "/settings/integrations", component: "./Settings" },
        { path: "/settings/transfers", component: "./Settings" },
        { path: "/settings/operations", component: "./Settings" },
        { path: "/settings/security", component: "./Settings" },
        { path: "/settings/workflows", component: "./Workflows" },
      ],
    },
  ],
  proxy: { "/api": { target: "http://127.0.0.1:9080", changeOrigin: true } },
});
