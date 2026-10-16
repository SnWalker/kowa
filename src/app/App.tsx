import { Link, Navigate, Route, Routes } from 'react-router-dom';
import { LoginPage } from '../auth/LoginPage';
import { RequireSession } from '../auth/RequireSession';
import { RuntimeDetailPage } from '../runtimes/RuntimeDetailPage';
import { RuntimesPage } from '../runtimes/RuntimesPage';
import { Button } from '../ui/button';
import { EmptyState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { WorkItemCreatePage } from '../workitems/WorkItemCreatePage';
import { WorkItemDetailPage } from '../workitems/WorkItemDetailPage';
import { WorkItemsPage } from '../workitems/WorkItemsPage';
import { WorkspaceSettingsPage } from '../workspaces/WorkspaceSettingsPage';
import { WorkspacesPage } from '../workspaces/WorkspacesPage';
import { AppShell } from './AppShell';

export function App() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<Navigate to="/workspaces" replace />} />
        <Route path="login" element={<LoginPage />} />
        <Route element={<RequireSession />}>
          <Route path="workspaces" element={<WorkspacesPage />} />
          <Route path="workspaces/:workspaceId/settings" element={<WorkspaceSettingsPage />} />
          <Route path="workspaces/:workspaceId/work-items" element={<WorkItemsPage />} />
          <Route path="workspaces/:workspaceId/work-items/new" element={<WorkItemCreatePage />} />
          <Route path="workspaces/:workspaceId/work-items/:workItemId" element={<WorkItemDetailPage />} />
          <Route path="workspaces/:workspaceId/runtimes" element={<RuntimesPage />} />
          <Route path="workspaces/:workspaceId/runtimes/:runtimeId" element={<RuntimeDetailPage />} />
        </Route>
        <Route path="*" element={
          <Page title="页面不存在">
            <p className="mb-4 max-w-[80ch] text-sm text-muted-foreground">这个地址暂时没有可显示的内容。</p>
            <EmptyState title="没有对应的页面">
              <p>可以返回工作区继续操作。</p>
            </EmptyState>
            <div className="mt-4">
              <Button asChild variant="secondary">
                <Link to="/workspaces">返回工作区</Link>
              </Button>
            </div>
          </Page>
        } />
      </Route>
    </Routes>
  );
}
