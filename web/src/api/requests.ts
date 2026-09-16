async function call<T>(path:string,options?:RequestInit):Promise<T>{const response=await fetch(path,{credentials:'same-origin',...options,headers:{...(options?.body instanceof Blob?{}:{'Content-Type':'application/json'}),...(options?.headers||{})}});if(response.status===204)return undefined as T;const data=await response.json().catch(()=>({}));if(!response.ok)throw new Error(data.detail||'请求失败');return data}
const key=()=>crypto.randomUUID();
export const requestAPI={
  bootstrap:()=>call<any>('/api/v1/bootstrap'),list:()=>call<{items:any[]}>('/api/v1/requests'),
  progress:(requestId:string)=>call<any>(`/api/v1/requests/${requestId}/progress`),
  readiness:(params:URLSearchParams)=>call<any>(`/api/v1/requests/readiness?${params}`),
  create:(body:any)=>call<any>('/api/v1/requests',{method:'POST',headers:{'Idempotency-Key':key()},body:JSON.stringify(body)}),
  updatePurpose:(requestId:string,purpose:string,expected_version:number)=>call<any>(`/api/v1/requests/${requestId}/purpose`,{method:'PATCH',body:JSON.stringify({purpose,expected_version})}),
  aiSuggestion:(requestId:string,text:string)=>call<any>(`/api/v1/requests/${requestId}/ai-suggestions`,{method:'POST',body:JSON.stringify({text})}),
  reserve:(requestId:string,body:any)=>call<any>(`/api/v1/requests/${requestId}/upload-sessions`,{method:'POST',headers:{'Idempotency-Key':key()},body:JSON.stringify(body)}),
  upload:(sessionId:string,file:File)=>call<any>(`/data/v1/upload-sessions/${sessionId}/content`,{method:'PUT',headers:{'Content-Type':file.type||'application/octet-stream'},body:file}),
  submit:(requestId:string,expected_version:number)=>call<any>(`/api/v1/requests/${requestId}/submit`,{method:'POST',headers:{'Idempotency-Key':key()},body:JSON.stringify({expected_version,declaration_accepted:true})}),
  checks:(requestId:string)=>call<{items:any[]}>(`/api/v1/requests/${requestId}/checks`)
};
