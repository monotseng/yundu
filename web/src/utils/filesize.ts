export function formatFileSize(value: number | string | null | undefined): string {
  if (value === null || value === undefined || value === "") return "—";
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes === 0) return "0 B";

  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const size = bytes / 1024 ** index;
  const digits = index === 0 ? 0 : size < 10 ? 2 : size < 100 ? 1 : 0;
  return `${size.toLocaleString("zh-CN", { minimumFractionDigits: digits, maximumFractionDigits: digits })} ${units[index]}`;
}
