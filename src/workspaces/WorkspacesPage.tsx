import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { newRequestId } from '../api/session';
import { requireCsrf, useSession } from '../auth/session-context';
import { Button } from '../ui/button';
import { Field } from '../ui/Field';
import { Input } from '../ui/input';
import { Page } from '../ui/Page';
import { ErrorNotice, Notice, Section } from '../ui/States';

/**
 * Workspace entry. `kowa.identity-workspace.v1` has no collection query, so the
 * page offers the two operations the frozen contract does provide: create a
 * Workspace through `workspace.bootstrap`, or open a known Workspace id that
 * the server re-authorizes on every read. It never claims to list the current
 * user's memberships.
 */
export function WorkspacesPage() {
  const { state, client } = useSession();
  const navigate = useNavigate();
  const [name, setName] = useState('');
  const [workspaceId, setWorkspaceId] = useState('');
  const [failure, setFailure] = useState<unknown>(null);
  const [pending, setPending] = useState(false);

  async function create(event: React.FormEvent) {
    event.preventDefault();
    setFailure(null);
    setPending(true);
    try {
      const csrf = requireCsrf(state);
      const created = await client.bootstrapWorkspace(name.trim(), csrf, newRequestId('workspace'));
      setName('');
      navigate(`/workspaces/${encodeURIComponent(created.workspaceId)}/settings`);
    } catch (error) {
      setFailure(error);
    } finally {
      setPending(false);
    }
  }

  function open(event: React.FormEvent) {
    event.preventDefault();
    const id = workspaceId.trim();
    if (id === '') return;
    navigate(`/workspaces/${encodeURIComponent(id)}/settings`);
  }

  return (
    <Page title="Workspace">
      <p className="mb-4 max-w-[80ch] text-sm text-muted-foreground">
        创建新的工作区，或输入团队共享的工作区 ID 进入。是否可访问由服务端在每次读取时判定。
      </p>
      {failure !== null && <ErrorNotice title="创建工作区失败" error={failure} />}
      <div className="grid items-start gap-4 lg:grid-cols-2">
        <Section title="创建工作区" id="create-workspace">
          <form onSubmit={(event) => void create(event)}>
            <Field label="名称">
              <Input
                value={name}
                onChange={(event) => setName(event.target.value)}
                required
                maxLength={120}
                autoComplete="off"
              />
            </Field>
            <div className="mt-1 flex flex-wrap items-center gap-2">
              <Button type="submit" disabled={pending || name.trim() === ''}>
                {pending ? '正在创建…' : '创建并进入设置'}
              </Button>
            </div>
          </form>
        </Section>
        <Section title="进入已知工作区" id="open-workspace">
          <form onSubmit={open}>
            <Field label="Workspace ID">
              <Input
                value={workspaceId}
                onChange={(event) => setWorkspaceId(event.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <div className="mt-1 flex flex-wrap items-center gap-2">
              <Button type="submit" variant="secondary" disabled={workspaceId.trim() === ''}>打开</Button>
            </div>
          </form>
          <Notice tone="info" title="关于 ID">
            <p>ID 来自创建工作区的结果或团队共享的地址；是否可访问由服务端判定，本地输入不是访问凭证。</p>
          </Notice>
        </Section>
      </div>
    </Page>
  );
}
