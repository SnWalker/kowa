import { useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ApiError, type ApiClient } from '../api/client';
import type { CreateWorkItemCommand } from '../api/contracts';
import { newRequestId } from '../api/session';
import { useResource } from '../api/useResource';
import { requireCsrf, useSession } from '../auth/session-context';
import { LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { ErrorNotice, Notice, Section } from '../ui/States';

const CONTENT_MAX_LENGTH = 20000;
const TITLE_MAX_LENGTH = 300;

/**
 * Work item intake: one requirement and one set of acceptance criteria for the
 * single writable project repository the Workspace binds.
 *
 * There is no template choice, no second writable repository and no knowledge
 * search: the MVP creates the item and offers exactly one fixed, versioned
 * workflow afterwards. A pre-filled field is not an approval — the knowledge
 * binding is confirmed explicitly on the next page.
 */
export function WorkItemCreatePage() {
  const { workspaceId = '' } = useParams();
  const { state, client, cacheEpoch, generation } = useSession();
  const navigate = useNavigate();
  const scope = `${workspaceId}:${state.identity?.githubUserId ?? ''}:${cacheEpoch}`;
  const workspace = useResource(
    (signal) => client.getWorkspace(workspaceId, signal),
    [workspaceId, state.identity?.githubUserId, cacheEpoch],
    { scope, generation },
  );

  if (workspace.status === 'loading') return <LoadingState />;
  if (workspace.status === 'error') {
    return (
      <Page title="新建工作项">
        <ErrorNotice title="无法读取 Workspace 配置" error={workspace.error} />
        <div className="actions">
          <button type="button" onClick={workspace.reload}>重试</button>
        </div>
      </Page>
    );
  }
  const config = workspace.data;
  if (!config) return <LoadingState />;

  return (
    <Page title="新建工作项">
      <p className="page-intro">
        Workspace <code>{config.workspaceId}</code> · {config.name}。创建只登记目标与需求版本，不会启动流程；
        启动唯一固定流程是下一步的显式动作。
      </p>
      <Section title="目标与流程" id="target">
        <dl className="data-list">
          <div className="data-row">
            <dt>目标仓库（唯一可写绑定）</dt>
            <dd>
              {config.project
                ? `${config.project.displayName}（${config.project.githubRepositoryId}，${config.project.access}）`
                : '尚未登记项目仓'}
            </dd>
          </div>
          <div className="data-row">
            <dt>知识仓库（只读）</dt>
            <dd>
              {config.knowledge
                ? `${config.knowledge.displayName}（${config.knowledge.githubRepositoryId}，${config.knowledge.access}）`
                : '尚未登记知识仓'}
            </dd>
          </div>
          <div className="data-row">
            <dt>流程</dt>
            <dd>唯一固定内置流程（版本化、冻结后运行）；没有模板选择，也不能在此指定第二个可写仓库。</dd>
          </div>
        </dl>
        {!config.project && (
          <Notice tone="warning" title="缺少项目仓绑定">
            <p>
              创建需要 Workspace 的项目仓绑定；请先在{' '}
              <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/settings`}>Workspace 设置</Link> 中登记。
              服务端会以 <code>POLICY_BLOCKED</code> 拒绝未绑定目标仓库的创建。
            </p>
          </Notice>
        )}
      </Section>
      <IntakeForm
        key={scope}
        client={client}
        workspaceId={workspaceId}
        canCreate={config.project !== undefined}
        csrfState={state}
        onCreated={(workItemId) => {
          navigate(`/workspaces/${encodeURIComponent(workspaceId)}/work-items/${encodeURIComponent(workItemId)}`);
        }}
      />
    </Page>
  );
}

type IntakeFormProps = {
  client: ApiClient;
  workspaceId: string;
  canCreate: boolean;
  csrfState: ReturnType<typeof useSession>['state'];
  onCreated: (workItemId: string) => void;
};

function IntakeForm({ client, workspaceId, canCreate, csrfState, onCreated }: IntakeFormProps) {
  const [title, setTitle] = useState('');
  const [requirement, setRequirement] = useState('');
  const [acceptance, setAcceptance] = useState('');
  const [failure, setFailure] = useState<unknown>(null);
  const [pending, setPending] = useState(false);
  // One intent keeps one request key: the same content submitted again is the
  // same delivery for the owner, while any edit is a new intent with a new key.
  const keys = useRef<{ fingerprint: string; key: string }>({ fingerprint: '', key: '' });

  function requestKey(content: Omit<CreateWorkItemCommand, 'requestKey'>): string {
    const fingerprint = JSON.stringify(content);
    if (keys.current.fingerprint !== fingerprint) {
      keys.current = { fingerprint, key: newRequestId('intake') };
    }
    return keys.current.key;
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setFailure(null);
    setPending(true);
    const content = {
      title: title.trim(),
      requirement: { version: 1, content: requirement },
      acceptance: { version: 1, content: acceptance },
    };
    const command: CreateWorkItemCommand = { requestKey: requestKey(content), ...content };
    try {
      const csrf = requireCsrf(csrfState);
      const created = await client.createWorkItem(workspaceId, command, csrf);
      keys.current = { fingerprint: '', key: '' };
      onCreated(created.workItem.workItemId);
    } catch (error) {
      setFailure(error);
      // A conflicting request key is a new intent: the next attempt must not
      // reuse the key the owner already refused with different content.
      if (error instanceof ApiError && error.category === 'VERSION_CONFLICT') {
        keys.current = { fingerprint: '', key: '' };
      }
    } finally {
      setPending(false);
    }
  }

  const complete = title.trim() !== '' && requirement !== '' && acceptance !== '';

  return (
    <Section title="需求与验收标准" id="intake">
      <p>
        只登记一条需求与一条验收标准（服务端冻结内容摘要与版本）。这里不预填批准、不选择知识条目，
        也不提供第二个可写仓库；这些输入不会绕过后续的人工确认。
      </p>
      {failure !== null && <ErrorNotice title="创建工作项失败" error={failure} />}
      <form onSubmit={(event) => void submit(event)}>
        <fieldset disabled={!canCreate || pending}>
          <legend>工作项</legend>
          <label className="field">
            <span>标题</span>
            <input
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              required
              maxLength={TITLE_MAX_LENGTH}
              autoComplete="off"
            />
          </label>
          <label className="field">
            <span>需求</span>
            <textarea
              value={requirement}
              onChange={(event) => setRequirement(event.target.value)}
              required
              maxLength={CONTENT_MAX_LENGTH}
              rows={6}
            />
          </label>
          <label className="field">
            <span>验收标准</span>
            <textarea
              value={acceptance}
              onChange={(event) => setAcceptance(event.target.value)}
              required
              maxLength={CONTENT_MAX_LENGTH}
              rows={6}
            />
          </label>
        </fieldset>
        {canCreate ? (
          <div className="actions">
            <button type="submit" disabled={pending || !complete}>
              {pending ? '正在创建…' : '创建工作项'}
            </button>
          </div>
        ) : (
          <Notice tone="info" title="暂时无法创建">
            <p>缺少项目仓绑定；请先在 Workspace 设置中登记目标仓库。</p>
          </Notice>
        )}
      </form>
    </Section>
  );
}
