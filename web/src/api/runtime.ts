export type Runtime = { product_name:string; product_short_name:string; product_tagline:string; portal_zone:'OFFICE'|'PRODUCTION'|'UNKNOWN'; version:string; limits:Record<string,number> };
export async function getRuntime(): Promise<Runtime> {
  const response = await fetch('/api/v1/runtime', { credentials:'same-origin' });
  if (!response.ok) throw new Error('运行配置加载失败');
  return response.json();
}
