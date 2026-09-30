import { Link, Route, Routes } from 'react-router-dom';
import { EmptyState } from '../ui/Feedback';
import { Page } from '../ui/Page';
import { AppShell } from './AppShell';

export function App() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={
          <Page title="欢迎使用 Kowa">
            <p className="page-intro">让每一步研发协作清晰可见。</p>
            <EmptyState title="这里还没有内容">
              <p>新的协作体验即将从这里展开。</p>
            </EmptyState>
          </Page>
        } />
        <Route path="*" element={
          <Page title="页面不存在">
            <p className="page-intro">这个地址暂时没有可显示的内容。</p>
            <Link className="button-link" to="/">返回首页</Link>
          </Page>
        } />
      </Route>
    </Routes>
  );
}
