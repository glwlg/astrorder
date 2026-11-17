import type { GroupFileReference } from "../../domain/types";

export function normalizeGroupFilePath(raw: string): string {
  let value = raw.trim().replace(/^file:\/\//, "");
  try { value = decodeURIComponent(value); } catch { /* Literal percent in filenames. */ }
  value = value.replace(/\\/g, "/").replace(/:\d+(?::\d+)?$/, "").split(/[?#]/, 1)[0];
  const prefix = value.startsWith("//") ? "//" : value.startsWith("/") ? "/" : "";
  const parts: string[] = [];
  for (const part of value.split("/")) {
    if (!part || part === ".") continue;
    if (part === ".." && parts.length && parts.at(-1) !== "..") parts.pop();
    else parts.push(part);
  }
  value = prefix + parts.join("/");
  return value;
}

export function findGroupFile(files: GroupFileReference[], raw: string): GroupFileReference | undefined {
  const value = normalizeGroupFilePath(raw);
  return files.find((file) => [file.path, ...file.aliases].some((path) => {
    const key = normalizeGroupFilePath(path);
    return /^[a-z]:\//i.test(key) || key.startsWith("//") ? key.toLowerCase() === value.toLowerCase() : key === value;
  }));
}
