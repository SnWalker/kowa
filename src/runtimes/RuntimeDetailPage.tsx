import { Link, useParams } from 'react-router-dom';
import { useResource } from '../api/useResource';
import { useSession } from '../auth/session-context';
import { LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { Badge, DataList, ErrorNotice, Section } from '../ui/States';
import { formatCapabilities, formatTimestamp, freshnessPresentation } from './presentation';

export function RuntimeDetailPage() {
  const { workspaceId = '', runtimeId = '' } = useParams();
  const { state, client, generation } = useSession();
  const resource = useResource(
    (signal) => client.getRuntime(workspaceId, runtimeId, signal),
    [workspaceId, runtimeId, state.identity?.githubUserId, generation],
  );

  if (resource.status === 'loading') return <LoadingState />;
  if (resource.status === 'error') {
    return (
      <Page title={`Runtime ${runtimeId}`}>
        <ErrorNotice title="无法读取 Runtime" error={resource.error} />
        <div className="actions">
          <button type="button" onClick={resource.reload}>重试</button>
          <Link className="button-link" to={`/workspaces/${encodeURIComponent(workspaceId)}/runtimes`}>返回列表</Link>
        </div>
      </Page>
    );
  }
  const runtime = resource.data?.runtime;
  if (!runtime) return <LoadingState />;
  const freshness = freshnessPresentation(runtime.freshness, runtime.staleReason);
  const observation = runtime.observation;

  return (
    <Page title={runtime.runtimeId}>
      <p className="page-intro">
        Provider 与能力分层是相互独立的机器事实；Runtime 上报不会获得 Workspace 可见性，
        可见性只来自该 Workspace 的执行配置。
      </p>
      <Section title="Runtime 状态" id="runtime-state">
        <p><Badge tone={freshness.tone}>{freshness.label}</Badge></p>
        <p className="page-note">{freshness.detail}</p>
        <DataList
          items={[
            { term: '登记机器 GitHub 用户 ID', value: runtime.gitHubUserId },
            { term: 'Provider', value: `${runtime.providerId} ${runtime.providerVersion}` },
            { term: 'Provider 认证', value: runtime.providerAuthenticated ? '已认证' : '未认证 / 未知' },
            { term: '能力', value: formatCapabilities(runtime.capabilities) },
            { term: '登记时间', value: formatTimestamp(runtime.registeredAt) },
          ]}
        />
      </Section>
      <Section title="最近观测" id="runtime-observation">
        {observation ? (
          <DataList
            items={[
              { term: '运行世代', value: observation.runtimeEpoch },
              { term: 'Runner 上报时间（观测）', value: formatTimestamp(observation.observedAt) },
              { term: '服务端水位（决定陈旧）', value: formatTimestamp(observation.receivedAt) },
              { term: 'CLI 版本', value: observation.cliVersion },
              { term: '可执行文件存在', value: observation.executable ? '是' : '否' },
              { term: '版本兼容', value: observation.versionCompatible ? '是' : '否' },
              { term: '已认证', value: observation.authenticated ? '是' : '否' },
              { term: '可执行（观测结论）', value: observation.canExecute ? '是' : '否' },
              { term: '说明', value: observation.detail ?? '无' },
            ]}
          />
        ) : (
          <p>该 Runtime 没有观测记录；未知状态不表示可用。</p>
        )}
      </Section>
      <div className="actions">
        <Link className="button-link" to={`/workspaces/${encodeURIComponent(workspaceId)}/runtimes`}>返回 Runtime 列表</Link>
      </div>
    </Page>
  );
}
