import { createContext } from "react";
import type { GroupFileReference } from "../../domain/types";

export interface FileSourceScope {
  groupId: string;
  files: GroupFileReference[];
  load: () => Promise<GroupFileReference[]>;
  checking?: boolean;
  error?: string;
  recheck?: () => Promise<void>;
}

export const FileSourceContext = createContext<FileSourceScope | null>(null);
