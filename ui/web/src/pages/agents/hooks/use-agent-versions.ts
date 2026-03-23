import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useWs } from "@/hooks/use-ws";
import { Methods } from "@/api/protocol";
import { queryKeys } from "@/lib/query-keys";
import { toast } from "@/stores/use-toast-store";
import i18n from "@/i18n";
import { userFriendlyError } from "@/lib/error-utils";

export interface AgentVersionSummary {
  version: number;
  displayName: string;
  provider: string;
  model: string;
  changedBy: string;
  changeSummary: string;
  createdAt: string;
}

export interface AgentVersionDetail extends AgentVersionSummary {
  frontmatter: string;
  contextWindow: number;
  maxToolIterations: number;
  workspace: string;
  restrictToWorkspace: boolean;
  toolsConfig: unknown;
  sandboxConfig: unknown;
  subagentsConfig: unknown;
  memoryConfig: unknown;
  compactionConfig: unknown;
  contextPruning: unknown;
  otherConfig: unknown;
  contextFiles: Array<{ file_name: string; content: string }> | null;
}

export function useAgentVersions(agentKey: string | undefined) {
  const ws = useWs();
  const queryClient = useQueryClient();

  const { data, isLoading: loading } = useQuery({
    queryKey: queryKeys.agents.versions(agentKey ?? ""),
    queryFn: async () => {
      if (!agentKey || !ws.isConnected) return { versions: [], total: 0 };
      const res = await ws.call<{ versions: AgentVersionSummary[]; total: number }>(
        Methods.AGENTS_VERSIONS_LIST,
        { agentId: agentKey, limit: 50 },
      );
      return { versions: res.versions ?? [], total: res.total ?? 0 };
    },
    enabled: !!agentKey && ws.isConnected,
  });

  const versions = data?.versions ?? [];
  const total = data?.total ?? 0;

  const getVersion = useCallback(
    async (version: number): Promise<AgentVersionDetail> => {
      if (!agentKey) throw new Error("agentKey is required");
      const res = await ws.call<AgentVersionDetail>(
        Methods.AGENTS_VERSIONS_GET,
        { agentId: agentKey, version },
      );
      return res;
    },
    [agentKey, ws],
  );

  const rollback = useCallback(
    async (version: number) => {
      if (!agentKey || !ws.isConnected) return;
      try {
        await ws.call(Methods.AGENTS_VERSIONS_ROLLBACK, { agentId: agentKey, version });
        queryClient.invalidateQueries({ queryKey: queryKeys.agents.versions(agentKey) });
        queryClient.invalidateQueries({ queryKey: queryKeys.agents.detail(agentKey) });
        queryClient.invalidateQueries({ queryKey: queryKeys.agents.all });
        toast.success(i18n.t("agents:versions.rollbackSuccess", { version }));
      } catch (err) {
        toast.error(i18n.t("agents:versions.rollbackFailed"), userFriendlyError(err));
        throw err;
      }
    },
    [agentKey, ws, queryClient],
  );

  const refresh = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: queryKeys.agents.versions(agentKey ?? "") });
  }, [queryClient, agentKey]);

  return { versions, total, loading, getVersion, rollback, refresh };
}
