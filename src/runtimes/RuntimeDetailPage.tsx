import { ArrowLeft } from 'lucide-react';
import { Link, useParams } from 'react-router-dom';
import { useResource } from '../api/useResource';
import { useSession } from '../auth/session-context';
import { Button } from '../ui/button';
import { LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { Badge, DataList, ErrorNotice, Section } from '../ui/States';
import { formatCapabilities, formatTimestamp, freshnessPresentation } from './presentation';

/**
 * One runtime's provider and capability facts next to its last observation.
 * Registration is not visibility and an observation is not availability, so
 * both are shown as separate machine facts.
 */
export function RuntimeDetailPage() {
  const { workspaceId = '', runtimeId = '' } = useParams();
  const { state, client, cacheEpoch, generation } = useSession();
  const resource = useResource(
    (signal) => client.getRuntime(workspaceId, runtimeId, signal),
    [workspaceId, runtimeId, state.identity?.githubUserId, cacheEpoch],
    { scope: `${workspaceId}:${runtimeId}:${state.identity?.githubUserId ?? ''}:${cacheEpoch}`, generation },
  );

  if (resource.status === 'loading') return <LoadingState />;
  if (resource.status === 'error') {
    return (
      <Page title={`Runtime ${runtimeId}`}>
        <ErrorNotice title="无法读取 Runtime" error={resource.error} />
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Button type="button" variant="secondary" onClick={resource.reload}>重试</Button>
          <Button asChild variant="ghost">
            <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/runtimes`}>返回列表</Link>
          </Button>
        </div>
      </Page>
    );
  }
  const runtime = resource.data?.runtime;
  if (!runtime) return <LoadingState />;
  const freshness = freshnessPresentation(runtime.freshness, runtime.staleReason);
  const observation = runtime.observation;

  return (
    <Page
      title={runtime.runtimeId}
      actions={(
        <Button asChild variant="secondary" size="sm">
          <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/runtimes`}>
            <ArrowLeft aria-hidden="true" />
            Runtime 列表
          </Link>
        </Button>
      )}
    >
      <p className="mb-4 max-w-[80ch] text-sm text-muted-foreground">
        Provider 与能力分层是相互独立的机器事实；Runtime 上报不会获得 Workspace 可见性，
        可见性只来自该 Workspace 的执行配置。
      </p>
      <Section title="Runtime 状态" id="runtime-state">
        <p className="flex flex-wrap items-center gap-2">
          <Badge tone={freshness.tone}>{freshness.label}</Badge>
          <span className="text-xs text-muted-foreground">{freshness.detail}</span>
        </p>
        <div className="mt-3">
          <DataList
            items={[
              { term: '登记机器 GitHub 用户 ID', value: <span className="font-mono">{runtime.gitHubUserId}</span> },
              { term: 'Provider', value: <span className="font-mono">{runtime.providerId} {runtime.providerVersion}</span> },
              { term: 'Provider 认证', value: runtime.providerAuthenticated ? '已认证' : '未认证 / 未知' },
              { term: '能力', value: <span className="font-mono">{formatCapabilities(runtime.capabilities)}</span> },
              { term: '登记时间', value: formatTimestamp(runtime.registeredAt) },
            ]}
          />
        </div>
      </Section>
      <Section title="最近观测" id="runtime-observation">
        {observation ? (
          <DataList
            items={[
              { term: '运行世代', value: <code>{observation.runtimeEpoch}</code> },
              { term: 'Runner 上报时间（观测）', value: formatTimestamp(observation.observedAt) },
              { term: '服务端水位（决定陈旧）', value: formatTimestamp(observation.receivedAt) },
              { term: 'CLI 版本', value: <code>{observation.cliVersion}</code> },
              { term: '可执行文件存在', value: observation.executable ? '是' : '否' },
              { term: '版本兼容', value: observation.versionCompatible ? '是' : '否' },
              { term: '已认证', value: observation.authenticated ? '是' : '否' },
              { term: '可执行（观测结论）', value: observation.canExecute ? '是' : '否' },
              { term: '说明', value: observation.detail ?? '无' },
            ]}
          />
        ) : (
          <p className="text-sm text-muted-foreground">该 Runtime 没有观测记录；未知状态不表示可用。</p>
        )}
      </Section>
    </Page>
  );
}
