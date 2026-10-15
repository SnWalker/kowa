import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { newRequestId } from '../api/session';
import { useSession, requireCsrf } from '../auth/session-context';
import { Page } from '../ui/Page';
import { ErrorNotice, Notice } from '../ui/States';

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
      <p className="page-intro">
        创建新的工作区，或输入团队共享的工作区 ID 进入。是否可访问由服务端在每次读取时判定。
      </p>
      {failure !== null && <ErrorNotice title="创建工作区失败" error={failure} />}
      <div className="panel-grid">
        <section className="panel" aria-labelledby="create-workspace">
          <h2 id="create-workspace">创建工作区</h2>
          <form onSubmit={(event) => void create(event)}>
            <label className="field">
              <span>名称</span>
              <input
                value={name}
                onChange={(event) => setName(event.target.value)}
                required
                maxLength={120}
                autoComplete="off"
              />
            </label>
            <div className="actions">
              <button type="submit" disabled={pending || name.trim() === ''}>
                {pending ? '正在创建…' : '创建并进入设置'}
              </button>
            </div>
          </form>
        </section>
        <section className="panel" aria-labelledby="open-workspace">
          <h2 id="open-workspace">进入已知工作区</h2>
          <form onSubmit={open}>
            <label className="field">
              <span>Workspace ID</span>
              <input
                value={workspaceId}
                onChange={(event) => setWorkspaceId(event.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </label>
            <div className="actions">
              <button type="submit" disabled={workspaceId.trim() === ''}>打开</button>
            </div>
          </form>
          <Notice tone="info" title="关于 ID">
            <p>ID 来自创建工作区的结果或团队共享的地址；是否可访问由服务端判定，本地输入不是访问凭证。</p>
          </Notice>
        </section>
      </div>
    </Page>
  );
}
