export const productName = '云渡文件交换平台';
export const productShortName = '云渡';
export const productTagline = '生产与办公网络文件交换及审批';

function BrandMark() {
  return <svg className="brand-mark" viewBox="0 0 44 44" role="img" aria-label="云渡标志">
    <rect width="44" height="44" rx="12" fill="#12384f" />
    <path d="M10 15.5h18.5l-3.8-3.8 2.9-2.9 8.8 8.8-8.8 8.8-2.9-2.9 3.8-3.8H10z" fill="#fff" />
    <path d="M34 28.5H15.5l3.8 3.8-2.9 2.9-8.8-8.8 8.8-8.8 2.9 2.9-3.8 3.8H34z" fill="#32c3b5" />
    <circle cx="10" cy="15.5" r="2.2" fill="#32c3b5" />
    <circle cx="34" cy="28.5" r="2.2" fill="#fff" />
  </svg>;
}

export default function Brand({ compact=false }: { compact?:boolean }) {
  return <div className={`brand ${compact ? 'brand-compact':''}`}>
    <BrandMark />
    {!compact && <div className="brand-copy"><strong>云渡</strong><span>文件交换平台</span></div>}
  </div>;
}
