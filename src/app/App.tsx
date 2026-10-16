import { lazy } from 'react';
import { Link, Navigate, Route, Routes } from 'react-router-dom';
import { RequireSession } from '../auth/RequireSession';
import { Button } from '../ui/button';
import { EmptyState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { AppShell } from './AppShell';

/**
 * The route table.
 *
 * Every page is loaded on demand: the shell, the session gate and the route
 * table are what the browser needs to paint the first screen, and each page's
 * own modules arrive when it is first opened. `AppShell` already wraps the
 * outlet in a `Suspense` boundary showing the product's own loading state, so a
 * page that is still arriving is stated as loading rather than rendered blank.
 *
 * A page in this list is only reachable through its route, so nothing else can
 * hold a static reference that would pull it back into the entry chunk — which
 * is what keeps the split real instead of nominal.
 */
const LoginPage = lazy(() => import('../auth/LoginPage').then((module) => ({ default: module.LoginPage })));
const WorkspacesPage = lazy(() => import('../workspaces/WorkspacesPage').then((module) => ({ default: module.WorkspacesPage })));
const WorkspaceSettingsPage = lazy(
  () => import('../workspaces/WorkspaceSettingsPage').then((module) => ({ default: module.WorkspaceSettingsPage })),
);
const WorkItemsPage = lazy(() => import('../workitems/WorkItemsPage').then((module) => ({ default: module.WorkItemsPage })));
const WorkItemCreatePage = lazy(
  () => import('../workitems/WorkItemCreatePage').then((module) => ({ default: module.WorkItemCreatePage })),
);
const WorkItemDetailPage = lazy(
  () => import('../workitems/WorkItemDetailPage').then((module) => ({ default: module.WorkItemDetailPage })),
);
const HumanTasksPage = lazy(() => import('../humantasks/HumanTasksPage').then((module) => ({ default: module.HumanTasksPage })));
const HumanTaskDetailPage = lazy(
  () => import('../humantasks/HumanTaskDetailPage').then((module) => ({ default: module.HumanTaskDetailPage })),
);
const RuntimesPage = lazy(() => import('../runtimes/RuntimesPage').then((module) => ({ default: module.RuntimesPage })));
const RuntimeDetailPage = lazy(
  () => import('../runtimes/RuntimeDetailPage').then((module) => ({ default: module.RuntimeDetailPage })),
);

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
          <Route path="workspaces/:workspaceId/human-tasks" element={<HumanTasksPage />} />
          <Route path="workspaces/:workspaceId/human-tasks/:humanTaskId" element={<HumanTaskDetailPage />} />
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
