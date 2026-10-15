import { Link, useParams } from 'react-router-dom';
import { useResource } from '../api/useResource';
import { useSession } from '../auth/session-context';
import { EmptyState, LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { Badge, ErrorNotice, Notice, Section } from '../ui/States';
import type { HumanTask, WorkItem } from '../api/contracts';
import { formatTimestamp } from '../runtimes/presentation';
import { openTaskCount, workItemStatePresentation } from './presentation';

/**
 * Work item list: the server projection of this Workspace's work items plus
 * the pending count of the HumanTasks the server reports for each of them.
 * The page computes no stage result of its own; a missing or failed pending
 * count is shown as unavailable instead of "0".
 */
export function WorkItemsPage() {
  const { workspaceId = '' } = useParams();
  const { state, client, cacheEpoch, generation } = useSession();
  const scope = `${workspaceId}:${state.identity?.githubUserId ?? ''}:${cacheEpoch}`;
  const deps = [workspaceId, state.identity?.githubUserId, cacheEpoch];
  const items = useResource((signal) => client.listWorkItems(workspaceId, signal), deps, { scope, generation });
  const tasks = useResource((signal) => client.listHumanTasks(workspaceId, signal), deps, { scope, generation });

  if (items.status === 'loading') return <LoadingState />;
  if (items.status === 'error') {
    return (
      <Page title="工作项">
        <ErrorNotice title="无法读取工作项列表" error={items.error} />
        <div className="actions">
          <button type="button" onClick={items.reload}>重试</button>
        </div>
      </Page>
    );
  }

  const workItems = items.data?.workItems ?? [];
  const anyRun = workItems.some((item) => item.currentRunId !== undefined);

  return (
    <Page title="工作项">
      <p className="page-intro">
        工作区 <code>{workspaceId}</code> 的工作项。列表、状态与待处理数都来自服务端投影；
        启动流程与知识确认在工作项详情内完成。
      </p>
      <div className="actions">
        <Link className="button-link" to={`/workspaces/${encodeURIComponent(workspaceId)}/work-items/new`}>
          新建工作项
        </Link>
      </div>
      {tasks.status === 'error' && (
        <ErrorNotice title="待处理任务数不可用" error={tasks.error} />
      )}
      <Section title="工作项列表" id="work-items">
        {workItems.length === 0 ? (
          <EmptyState title="还没有工作项">
            <p>创建第一个工作项后，这里会显示服务端返回的状态与待处理数。</p>
          </EmptyState>
        ) : (
          <div className="table-scroll">
            <table className="member-table">
              <caption>服务端返回的工作项（不在此处计算阶段结论）</caption>
              <thead>
                <tr>
                  <th scope="col">标题</th>
                  <th scope="col">状态</th>
                  <th scope="col">当前运行</th>
                  <th scope="col">待处理数</th>
                  <th scope="col">更新时间</th>
                </tr>
              </thead>
              <tbody>
                {workItems.map((item) => (
                  <tr key={item.workItemId}>
                    <td>
                      <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/work-items/${encodeURIComponent(item.workItemId)}`}>
                        {item.title}
                      </Link>
                    </td>
                    <td><WorkItemStateCell item={item} /></td>
                    <td>{item.currentRunId ?? '尚未启动'}</td>
                    <td><PendingCell tasks={tasks.status === 'ready' ? tasks.data?.humanTasks : undefined} item={item} /></td>
                    <td>{formatTimestamp(item.updatedAt)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {anyRun && (
          <Notice tone="info" title="关于运行状态">
            <p>
              列表只显示服务端给出的事件事实；节点进度、历史轮次与恢复动作属于后续阶段，不在此页推断。
            </p>
          </Notice>
        )}
      </Section>
    </Page>
  );
}

function WorkItemStateCell({ item }: { item: WorkItem }) {
  const presentation = workItemStatePresentation(item.state, item.currentRunId !== undefined);
  return <Badge tone={presentation.tone}>{presentation.label}</Badge>;
}

function PendingCell({ tasks, item }: { tasks?: HumanTask[]; item: WorkItem }) {
  if (tasks === undefined) return <span>待处理数不可用</span>;
  const count = openTaskCount(tasks, item.workItemId);
  if (count === 0) return <span>0</span>;
  return <Badge tone="warn">待处理 {count}</Badge>;
}
