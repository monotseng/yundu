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
export const adminAPI = {
  departments: () => call<{ items: any[] }>("/api/v1/admin/departments"),
  createDepartment: (body: any) =>
    call("/api/v1/admin/departments", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  groups: () => call<{ items: any[] }>("/api/v1/admin/groups"),
  createGroup: (body: any) =>
    call("/api/v1/admin/groups", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  updateGroup: (groupID: string, body: any) =>
    call<void>(`/api/v1/admin/groups/${groupID}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  users: () => call<{ items: any[] }>("/api/v1/admin/users"),
  createUser: (body: any) =>
    call<any>("/api/v1/admin/users", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  updateUser: (id:string,body:any)=>call<void>(`/api/v1/admin/users/${id}`,{method:"PATCH",body:JSON.stringify(body)}),
  reissueActivation: (id:string,expected_version:number)=>call<any>(`/api/v1/admin/users/${id}/activation`,{method:"POST",body:JSON.stringify({expected_version})}),
  initiateMFARecovery: (body:any)=>call<any>("/api/v1/admin/mfa-recoveries",{method:"POST",body:JSON.stringify(body)}),
  approveMFARecovery: (id:string,body:any)=>call<any>(`/api/v1/admin/mfa-recoveries/${id}/approve`,{method:"POST",body:JSON.stringify(body)}),
  roles: () => call<{ items: any[] }>("/api/v1/admin/roles"),
  assignRole: (userID: string, body: any) =>
    call<void>(`/api/v1/admin/users/${userID}/roles`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  addGroupMember: (groupID: string, body: any) =>
    call<void>(`/api/v1/admin/groups/${groupID}/members`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  businessSystems: () =>
    call<{ items: any[] }>("/api/v1/admin/business-systems"),
  createBusinessSystem: (body: any) =>
    call("/api/v1/admin/business-systems", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  integrations: () => call<{ items: any[] }>("/api/v1/admin/integrations"),
  createSecret: (body: any) =>
    call<any>("/api/v1/admin/secrets", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  createS3: (body: any) =>
    call("/api/v1/admin/integrations/s3", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  getS3: (id: string) => call<any>(`/api/v1/admin/integrations/${id}/s3`),
  updateS3: (id: string, body: any) => call<void>(`/api/v1/admin/integrations/${id}/s3`, { method: "PATCH", body: JSON.stringify(body) }),
  deleteIntegration: (id: string) => call<void>(`/api/v1/admin/integrations/${id}`, { method: "DELETE" }),
  testS3: (id: string) =>
    call(`/api/v1/admin/integrations/${id}/test`, {
      method: "POST",
      body: "{}",
    }),
  publish: (id: string, expected_version: number) =>
    call(`/api/v1/admin/integrations/${id}/publish`, {
      method: "POST",
      body: JSON.stringify({ expected_version }),
    }),
  channels: () => call<{ items: any[] }>("/api/v1/admin/exchange-channels"),
  publishChannel: (direction: string, body: any) =>
    call(`/api/v1/admin/exchange-channels/${direction}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  transfers: () => call<{ items: any[] }>("/api/v1/admin/transfers"),
  retryTransfer: (id: string) =>
    call(`/api/v1/admin/transfers/${id}/retry`, {
      method: "POST",
      headers: { "Idempotency-Key": crypto.randomUUID() },
      body: "{}",
    }),
};
