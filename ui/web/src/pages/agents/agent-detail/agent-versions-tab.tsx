import { useState, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { History, RotateCcw, Eye, ChevronDown, ChevronUp } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  useAgentVersions,
  type AgentVersionSummary,
  type AgentVersionDetail,
} from "../hooks/use-agent-versions";

interface AgentVersionsTabProps {
  agentKey: string;
}

export function AgentVersionsTab({ agentKey }: AgentVersionsTabProps) {
  const { t } = useTranslation("agents");
  const { versions, loading, getVersion, rollback, refresh } = useAgentVersions(agentKey);
  const [detailOpen, setDetailOpen] = useState(false);
  const [detail, setDetail] = useState<AgentVersionDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [rollbackOpen, setRollbackOpen] = useState(false);
  const [rollbackVersion, setRollbackVersion] = useState<number>(0);
  const [rollingBack, setRollingBack] = useState(false);

  const handleView = useCallback(
    async (version: number) => {
      setDetail(null);
      setDetailError(null);
      setDetailLoading(true);
      setDetailOpen(true);
      try {
        const v = await getVersion(version);
        setDetail(v);
      } catch (err) {
        const msg = err instanceof Error ? err.message : "Unknown error";
        setDetailError(msg);
      } finally {
        setDetailLoading(false);
      }
    },
    [getVersion],
  );

  const handleRollbackConfirm = useCallback(async () => {
    setRollingBack(true);
    try {
      await rollback(rollbackVersion);
      setRollbackOpen(false);
      refresh();
    } finally {
      setRollingBack(false);
    }
  }, [rollback, rollbackVersion, refresh]);

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        <div className="h-5 w-5 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent mr-2" />
        {t("versions.loading")}
      </div>
    );
  }

  if (versions.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-center">
        <History className="h-10 w-10 text-muted-foreground/50 mb-3" />
        <p className="text-sm font-medium text-muted-foreground">{t("versions.noVersions")}</p>
        <p className="text-xs text-muted-foreground/70 mt-1 max-w-xs">
          {t("versions.noVersionsDesc")}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {versions.map((v) => (
        <VersionRow
          key={v.version}
          version={v}
          onView={() => handleView(v.version)}
          onRollback={() => {
            setRollbackVersion(v.version);
            setRollbackOpen(true);
          }}
          t={t}
        />
      ))}

      {/* Version detail dialog */}
      <Dialog open={detailOpen} onOpenChange={setDetailOpen}>
        <DialogContent className="sm:max-w-2xl max-h-[85vh] flex flex-col">
          <DialogHeader>
            <DialogTitle>
              {t("versions.detailTitle", { version: detail?.version ?? "" })}
            </DialogTitle>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-y-auto space-y-3 -mx-4 px-4">
            {detailLoading ? (
              <div className="flex items-center justify-center py-8">
                <div className="h-5 w-5 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
              </div>
            ) : detailError ? (
              <div className="flex items-center justify-center py-8 text-sm text-destructive">
                {detailError}
              </div>
            ) : detail ? (
              <VersionDetailContent detail={detail} t={t} />
            ) : null}
          </div>
        </DialogContent>
      </Dialog>

      {/* Rollback confirmation dialog */}
      <Dialog open={rollbackOpen} onOpenChange={setRollbackOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("versions.rollbackTitle")}</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            {t("versions.rollbackDesc", { version: rollbackVersion })}
          </p>
          <DialogFooter className="gap-2 sm:gap-0">
            <Button variant="outline" onClick={() => setRollbackOpen(false)} disabled={rollingBack}>
              {t("versions.cancel")}
            </Button>
            <Button onClick={handleRollbackConfirm} disabled={rollingBack}>
              {rollingBack ? t("versions.rollingBack") : t("versions.rollbackConfirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// --- Sub-components ---

function VersionRow({
  version: v,
  onView,
  onRollback,
  t,
}: {
  version: AgentVersionSummary;
  onView: () => void;
  onRollback: () => void;
  t: (key: string) => string;
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border p-3 hover:bg-muted/30 transition-colors">
      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-bold">
        v{v.version}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="text-sm font-medium truncate">
            {v.changeSummary || t("versions.noSummary")}
          </span>
        </div>
        <div className="flex items-center gap-2 text-xs text-muted-foreground mt-0.5">
          <span>{v.model}</span>
          <span>&middot;</span>
          <span>{v.changedBy}</span>
          <span>&middot;</span>
          <time>{formatDate(v.createdAt)}</time>
        </div>
      </div>
      <div className="flex items-center gap-1 shrink-0">
        <Button variant="ghost" size="sm" onClick={onView} title={t("versions.view")}>
          <Eye className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="sm" onClick={onRollback} title={t("versions.rollback")}>
          <RotateCcw className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}

function VersionDetailContent({
  detail: v,
  t,
}: {
  detail: AgentVersionDetail;
  t: (key: string) => string;
}) {
  return (
    <>
      <DetailField label={t("versions.fieldModel")} value={`${v.provider} / ${v.model}`} />
      {v.displayName && <DetailField label={t("versions.fieldName")} value={v.displayName} />}
      {v.workspace && <DetailField label={t("versions.fieldWorkspace")} value={v.workspace} />}
      {v.changeSummary && (
        <DetailField label={t("versions.fieldChanges")} value={v.changeSummary} />
      )}
      <DetailField label={t("versions.fieldChangedBy")} value={v.changedBy} />
      <DetailField label={t("versions.fieldDate")} value={formatDate(v.createdAt)} />

      {/* JSON configs */}
      {v.toolsConfig && <JsonSection label={t("versions.fieldToolsConfig")} data={v.toolsConfig} />}
      {v.sandboxConfig && <JsonSection label={t("versions.fieldSandboxConfig")} data={v.sandboxConfig} />}
      {v.memoryConfig && <JsonSection label={t("versions.fieldMemoryConfig")} data={v.memoryConfig} />}
      {v.otherConfig && <JsonSection label={t("versions.fieldOtherConfig")} data={v.otherConfig} />}

      {/* Context files */}
      {v.contextFiles && v.contextFiles.length > 0 && (
        <div className="space-y-2 pt-2 border-t">
          <h4 className="text-sm font-medium">{t("versions.fieldContextFiles")}</h4>
          {v.contextFiles.map((f) => (
            <CollapsibleFile key={f.file_name} name={f.file_name} content={f.content} />
          ))}
        </div>
      )}
    </>
  );
}

function DetailField({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start gap-2">
      <span className="text-xs font-medium text-muted-foreground w-24 shrink-0 pt-0.5">
        {label}
      </span>
      <span className="text-sm break-all">{value}</span>
    </div>
  );
}

function JsonSection({ label, data }: { label: string; data: unknown }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="border rounded-md overflow-hidden">
      <button
        type="button"
        className="flex items-center justify-between w-full px-3 py-2 text-xs font-medium text-muted-foreground hover:bg-muted/30 cursor-pointer"
        onClick={() => setOpen(!open)}
      >
        {label}
        {open ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
      </button>
      {open && (
        <pre className="px-3 py-2 text-xs bg-muted/20 overflow-x-auto whitespace-pre-wrap border-t">
          {JSON.stringify(data, null, 2)}
        </pre>
      )}
    </div>
  );
}

function CollapsibleFile({ name, content }: { name: string; content: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="border rounded-md overflow-hidden">
      <button
        type="button"
        className="flex items-center justify-between w-full px-3 py-2 text-xs font-medium hover:bg-muted/30 cursor-pointer"
        onClick={() => setOpen(!open)}
      >
        {name}
        {open ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
      </button>
      {open && (
        <pre className="px-3 py-2 text-xs bg-muted/20 overflow-x-auto whitespace-pre-wrap border-t max-h-60 overflow-y-auto">
          {content}
        </pre>
      )}
    </div>
  );
}

function formatDate(iso: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}
