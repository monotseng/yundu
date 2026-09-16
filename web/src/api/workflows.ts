async function call<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(options?.headers || {}),
    },
  });
  if (response.status === 204) return undefined as T;
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.detail || "请求失败");
  return data;
}
const key = () => crypto.randomUUID();
export const workflowAPI = {
  list: () => call<{ items: any[] }>("/api/v1/admin/workflows"),
  create: (body: any) =>
    call<any>("/api/v1/admin/workflows", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  version: (id: string) => call<any>(`/api/v1/admin/workflow-versions/${id}`),
  update: (id: string, body: any) => call<void>(`/api/v1/admin/workflow-versions/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  clone: (id: string) =>
    call<any>(`/api/v1/admin/workflows/${id}/versions`, {
      method: "POST",
      body: "{}",
    }),
  validate: (id: string) =>
    call<any>(`/api/v1/admin/workflow-versions/${id}/validate`, {
      method: "POST",
      body: "{}",
    }),
  simulate: (id: string, input: any) =>
    call<any>(`/api/v1/admin/workflow-versions/${id}/simulate`, {
      method: "POST",
      body: JSON.stringify({ input }),
    }),
  publish: (id: string, note: string) =>
    call<void>(`/api/v1/admin/workflow-versions/${id}/publish`, {
      method: "POST",
      body: JSON.stringify({ change_note: note }),
    }),
  bind: (body: any) =>
    call<void>("/api/v1/admin/workflow-bindings", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  bindings: () => call<{ items: any[] }>("/api/v1/admin/workflow-bindings"),
  tasks: () => call<{ items: any[] }>("/api/v1/approval-tasks"),
  decide: (id: string, body: any) =>
    call<void>(`/api/v1/approval-tasks/${id}/decision`, {
      method: "POST",
      headers: { "Idempotency-Key": key() },
      body: JSON.stringify(body),
    }),
};
