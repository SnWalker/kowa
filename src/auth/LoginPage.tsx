import { useState } from 'react';
import { Navigate, useSearchParams } from 'react-router-dom';
import { Button } from '../ui/button';
import { Card, CardContent } from '../ui/card';
import { Page } from '../ui/Page';
import { ErrorNotice, Notice } from '../ui/States';
import { safeReturnTo } from './returnTo';
import { useSession } from './session-context';

/**
 * The only entry into the product. The browser never holds a credential: it
 * starts the owner-produced authorization URL and, afterwards, keeps only the
 * server's opaque session cookie.
 */
export function LoginPage() {
  const { state, login } = useSession();
  const [params] = useSearchParams();
  const [failure, setFailure] = useState<unknown>(null);
  const [pending, setPending] = useState(false);
  const returnTo = safeReturnTo(params.get('returnTo') ?? '/workspaces');

  if (state.status === 'authenticated') return <Navigate to={returnTo} replace />;

  async function begin() {
    setFailure(null);
    setPending(true);
    try {
      await login(returnTo);
    } catch (error) {
      setPending(false);
      setFailure(error);
    }
  }

  return (
    <Page title="登录 Kowa">
      <Card className="max-w-[42rem] gap-0 rounded-md py-5 shadow-none">
        <CardContent className="px-5">
          <p className="text-sm text-muted-foreground">
            使用 GitHub 身份登录。Kowa 在服务端建立会话，浏览器只持有不透明的 HttpOnly 会话 Cookie；
            GitHub 授权完成后会回到 {returnTo}。
          </p>
          {state.status === 'unavailable' && (
            <Notice tone="warning" title="身份服务当前不可用">
              <p>{state.message}</p>
            </Notice>
          )}
          {failure !== null && <ErrorNotice title="无法开始登录" error={failure} />}
          <div className="mt-4 flex flex-wrap items-center gap-2">
            <Button type="button" size="lg" onClick={() => void begin()} disabled={pending}>
              {pending ? '正在跳转…' : '使用 GitHub 登录'}
            </Button>
          </div>
          <p className="mt-4 border-t border-border-muted pt-3 text-xs text-muted-foreground">
            会话失效、被撤销或过期后需要重新登录；Workspace 权限和人工任务权限由服务端单独判定，
            登录成功或安装 GitHub App 都不会自动获得管理员权限。
          </p>
        </CardContent>
      </Card>
    </Page>
  );
}
