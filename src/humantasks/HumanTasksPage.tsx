import { Link, useParams } from 'react-router-dom';
import { useState } from 'react';
import { useSession } from '../auth/session-context';
import { useResource } from '../api/useResource';
import type { HumanTask } from '../api/contracts';
import { Button } from '../ui/button';
import { CheckboxField } from '../ui/Field';
import { EmptyState, LoadingState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { Badge, DataList, ErrorNotice, Section } from '../ui/States';
import { humanTaskStatePresentation } from '../workitems/presentation';
import { formatTimestamp } from '../runtimes/presentation';
import { actionPresentation, completionModePresentation } from './presentation';

/**
 * The workspace's human requests: everything the server is waiting on a person
 * for, with the server's own action judgement per row.
 *
 * The list is a projection, never a queue this page maintains: the "waiting"
 * filter is the owner's `?state=open`, and a read failure is reported as a
 * failure instead of an empty queue.
 */
export function HumanTasksPage() {
  const { workspaceId = '' } = useParams();
  const { state, client, cacheEpoch, generation } = useSession();
  const scope = `${workspaceId}:human-tasks:${state.identity?.githubUserId ?? ''}:${cacheEpoch}`;
  const [openOnly, setOpenOnly] = useState(false);
  const tasks = useResource(
    (signal) => client.listHumanTasks(workspaceId, { openOnly, signal }),
    [workspaceId, state.identity?.githubUserId, cacheEpoch, openOnly],
    { scope: `${scope}:${openOnly ? 'open' : 'all'}`, generation },
  );

  return (
    <Page title="人工待办" meta={<>工作区 <code>{workspaceId}</code></>}>
      <Section title="待办清单" id="human-tasks">
        <p className="mb-3 max-w-[80ch] text-sm text-muted-foreground">
          每条都是服务端持久化的人工请求，Web 只是渲染并提交同一份服务端定义。是否可答复由服务端按当前成员、
          请求状态与所审对象版本判定；本页不代替它做权限判断。
        </p>
        <div className="mb-3">
          <CheckboxField
            id="human-tasks-open-only"
            ariaLabel="只看未决请求"
            checked={openOnly}
            onCheckedChange={(checked) => setOpenOnly(checked)}
          >
            只看未决（服务端 state=open 筛选）
          </CheckboxField>
        </div>
        {tasks.status === 'loading' && <LoadingState />}
        {tasks.status === 'error' && (
          <>
            <ErrorNotice title="无法读取人工待办" error={tasks.error} />
            <div className="mt-3">
              <Button type="button" variant="secondary" onClick={tasks.reload}>重试</Button>
            </div>
          </>
        )}
        {tasks.status === 'ready' && tasks.data !== undefined && (
          tasks.data.humanTasks.length === 0 ? (
            <EmptyState title={openOnly ? '没有未决的人工待办' : '没有人工待办'}>
              <p>
                这是成功读取后的结果，不是读取失败。{openOnly ? '取消筛选可以看到已解决、已过期与已取消的请求。' : ''}
              </p>
            </EmptyState>
          ) : (
            <ul className="flex flex-col gap-3">
              {tasks.data.humanTasks.map((task) => (
                <li key={task.humanTaskId}>
                  <TaskRow workspaceId={workspaceId} task={task} />
                </li>
              ))}
            </ul>
          )
        )}
      </Section>
    </Page>
  );
}

function TaskRow({ workspaceId, task }: { workspaceId: string; task: HumanTask }) {
  const state = humanTaskStatePresentation(task.state);
  const mode = completionModePresentation(task.request.completionMode);
  const action = actionPresentation(task);
  return (
    <div className="rounded-md border border-border-muted px-3 py-2">
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold text-foreground">
            <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/human-tasks/${encodeURIComponent(task.humanTaskId)}`}>
              {task.request.title}
            </Link>
          </h3>
          <p className="mt-0.5 text-xs text-muted-foreground">
            <code>{task.request.kind}</code> · {mode.label} · 请求版本 v{task.request.version} · 任务版本 v{task.version}
          </p>
        </div>
        <Badge tone={state.tone}>{state.label}</Badge>
      </div>
      <DataList
        items={[
          { term: '工作项', value: <Link to={`/workspaces/${encodeURIComponent(workspaceId)}/work-items/${encodeURIComponent(task.workItemId)}`}><code>{task.workItemId}</code></Link> },
          { term: '运行 / 节点', value: <code>{task.workflowRunId ?? '（未登记）'} · {task.nodeRunId ?? '（未登记）'}</code> },
          { term: '所审版本', value: <code>{task.request.object.kind} · {task.request.object.id}@{task.request.object.version}</code> },
          { term: '服务端动作判定', value: action.detail },
          { term: '创建 / 更新', value: `${formatTimestamp(task.createdAt)} · ${formatTimestamp(task.updatedAt)}` },
        ]}
      />
    </div>
  );
}
