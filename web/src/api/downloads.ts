async function call<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(options?.headers || {}),
    },
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.detail || "请求失败");
  return data;
}
export const downloadAPI = {
  available: () => call<{ items: any[] }>("/api/v1/downloads/available"),
  grant: (fileId: string, expectedVersion: number) =>
    call<{ grant: string; expires_at: string }>(
      `/api/v1/files/${fileId}/download-grants`,
      {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID() },
        body: JSON.stringify({ expected_version: expectedVersion }),
      },
    ),
  receipts: (requestId: string) =>
    call<{ items: any[] }>(`/api/v1/requests/${requestId}/downloads`),
};
