import { Plus } from 'lucide-react';
import { Link, useParams } from 'react-router-dom';
import { useResource } from '../api/useResource';
import { useSession } from '../auth/session-context';
import { Button } from '../ui/button';
import { EmptyState, LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { Badge, ErrorNotice, Notice, Section } from '../ui/States';
import {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../ui/table';
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
        <div className="mt-3">
          <Button type="button" variant="secondary" onClick={items.reload}>重试</Button>
        </div>
      </Page>
    );
  }

  const workItems = items.data?.workItems ?? [];
  const anyRun = workItems.some((item) => item.currentRunId !== undefined);

  return (
    <Page
      title="工作项"
      meta={<>工作区 <code>{workspaceId}</code></>}
      actions={(
        <Button asChild size="sm">
          <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/work-items/new`}>
            <Plus aria-hidden="true" />
            新建工作项
          </Link>
        </Button>
      )}
    >
      <p className="mb-4 max-w-[80ch] text-sm text-muted-foreground">
        列表、状态与待处理数都来自服务端投影；启动流程与知识确认在工作项详情内完成。
      </p>
      {tasks.status === 'error' && (
        <ErrorNotice title="待处理任务数不可用" error={tasks.error} />
      )}
      <Section title="工作项列表" id="work-items">
        {workItems.length === 0 ? (
          <EmptyState title="还没有工作项">
            <p>创建第一个工作项后，这里会显示服务端返回的状态与待处理数。</p>
          </EmptyState>
        ) : (
          <Table>
            <TableCaption className="caption-top mb-2 mt-0 text-left text-xs text-muted-foreground">
              服务端返回的工作项（不在此处计算阶段结论）
            </TableCaption>
            <TableHeader className="bg-muted">
              <TableRow>
                <TableHead scope="col">标题</TableHead>
                <TableHead scope="col">状态</TableHead>
                <TableHead scope="col">当前运行</TableHead>
                <TableHead scope="col">待处理数</TableHead>
                <TableHead scope="col">更新时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {workItems.map((item) => (
                <TableRow key={item.workItemId}>
                  <TableCell className="whitespace-normal">
                    <Link className="no-underline" to={`/workspaces/${encodeURIComponent(workspaceId)}/work-items/${encodeURIComponent(item.workItemId)}`}>
                      {item.title}
                    </Link>
                  </TableCell>
                  <TableCell className="whitespace-normal"><WorkItemStateCell item={item} /></TableCell>
                  <TableCell className="whitespace-normal font-mono">{item.currentRunId ?? '尚未启动'}</TableCell>
                  <TableCell className="whitespace-normal"><PendingCell tasks={tasks.status === 'ready' ? tasks.data?.humanTasks : undefined} item={item} /></TableCell>
                  <TableCell className="whitespace-normal">{formatTimestamp(item.updatedAt)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
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
