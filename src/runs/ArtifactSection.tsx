import type { ArtifactRecord, RunBinding } from '../api/contracts';
import { formatTimestamp } from '../runtimes/presentation';
import { EmptyState } from '../ui/Feedback';
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '../ui/table';
import { Badge } from '../ui/States';
import { artifactStatePresentation, formatBytes } from './presentation';

/**
 * The published deliverables, as references.
 *
 * No browser-facing operation in the frozen contracts serves artifact content,
 * so this section offers no download and no constructed URL: it publishes the
 * identity and the digest the operator needs to ask the owner for the bytes,
 * and it says so rather than inventing a link that would 404.
 */
export function ArtifactSection({
  artifacts,
  runBindings,
}: {
  artifacts: ArtifactRecord[];
  runBindings: RunBinding[];
}) {
  return (
    <>
      {artifacts.length === 0 ? (
        <EmptyState title="还没有已发布产物">
          本次工作项未返回任何 Artifact 记录。空清单是成功读取后的结果，不代表读取失败，也不代表后续不会产生产物。
        </EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableCaption className="caption-top mb-2 mt-0 text-left text-xs text-muted-foreground">
              服务端发布的交付物（只读引用；本契约不提供浏览器端的内容读取操作）
            </TableCaption>
            <TableHeader className="bg-muted">
              <TableRow>
                <TableHead>产物</TableHead>
                <TableHead>种类</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>摘要</TableHead>
                <TableHead>大小 / 类型</TableHead>
                <TableHead>产出者</TableHead>
                <TableHead>创建时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {artifacts.map((artifact) => {
                const state = artifactStatePresentation(artifact.state);
                return (
                  <TableRow key={artifact.artifactId}>
                    <TableCell className="whitespace-normal">
                      <code>{artifact.artifactId}</code>
                      <span className="block text-xs text-muted-foreground">对象 {artifact.subjectId}</span>
                    </TableCell>
                    <TableCell className="whitespace-normal">
                      <code>{artifact.kind}</code>
                      {artifact.requiresApproval ? (
                        <span className="block text-xs text-muted-foreground">需要人工批准</span>
                      ) : null}
                    </TableCell>
                    <TableCell className="whitespace-normal">
                      <Badge tone={state.tone}>{state.label}</Badge>
                    </TableCell>
                    <TableCell className="whitespace-normal">
                      <code className="block max-w-[16rem] truncate">{artifact.digest}</code>
                    </TableCell>
                    <TableCell className="whitespace-normal">
                      {formatBytes(artifact.size)}
                      <span className="block text-xs text-muted-foreground">{artifact.mediaType}</span>
                    </TableCell>
                    <TableCell className="whitespace-normal">
                      <code>{artifact.producerId}</code>
                      <span className="block text-xs text-muted-foreground">由 {artifact.producerKind} 产出</span>
                    </TableCell>
                    <TableCell className="whitespace-normal">{formatTimestamp(artifact.createdAt)}</TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}

      <div className="mt-4">
        <h3 className="mb-2 text-sm font-semibold text-foreground">运行绑定与修复谱系</h3>
        {runBindings.length === 0 ? (
          <p className="text-sm text-muted-foreground">工作项当前没有运行绑定记录。</p>
        ) : (
          <ul className="space-y-2">
            {runBindings.map((binding) => (
              <li key={`${binding.workflowRunId}:${binding.boundAt}`} className="rounded-md border border-border-muted px-3 py-2">
                <p className="flex flex-wrap items-center gap-2 text-sm">
                  <Badge tone={binding.kind === 'repair' ? 'warn' : 'unknown'}>
                    {binding.kind === 'repair' ? '修复运行（repair）' : '初始运行（initial）'}
                  </Badge>
                  <code>{binding.workflowRunId}</code>
                </p>
                {binding.sourceRunId ? (
                  <p className="mt-1 text-xs text-muted-foreground">
                    源运行 <code>{binding.sourceRunId}</code>
                    {binding.mergedBaselineOid ? (
                      <>
                        {' '}· 合并基线 <code>{binding.mergedBaselineOid}</code>
                      </>
                    ) : null}
                  </p>
                ) : null}
                {binding.reason ? <p className="mt-1 text-xs text-muted-foreground">原因：{binding.reason}</p> : null}
                <p className="mt-1 text-xs text-muted-foreground">
                  由 {binding.boundByGitHubUserId} 于 {formatTimestamp(binding.boundAt)} 绑定
                </p>
              </li>
            ))}
          </ul>
        )}
      </div>
    </>
  );
}
