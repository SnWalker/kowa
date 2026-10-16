import { RefreshCw } from 'lucide-react';
import { Button } from '../ui/button';
import { Notice } from '../ui/States';
import { formatTimestamp } from '../runtimes/presentation';

/**
 * The state of the live read, in words.
 *
 * A disconnected page keeps showing the last projection it really read — the
 * operator still needs it — but it says so next to the watermark, so the
 * retained projection is never mistaken for the current fact. "No new facts
 * yet" and "the read is broken" are different states and are never merged.
 */
export function LiveStatus({
  disconnected,
  lastObservedAt,
  onRetry,
}: {
  disconnected: boolean;
  lastObservedAt?: string;
  onRetry: () => void;
}) {
  const observed = lastObservedAt === undefined ? undefined : formatTimestamp(lastObservedAt);

  if (disconnected) {
    return (
      <Notice tone="warning" title="实时更新已断开">
        <p>
          浏览器与当前投影之间的实时更新已中断；最后观测 <code>{observed ?? '（无成功观测）'}</code>，此后的事实未知。
          下方展示的是断开前读到的内容，未包含断开之后服务端可能已经产生的变化。
        </p>
        <div className="mt-2">
          <Button type="button" variant="secondary" onClick={onRetry}>
            <RefreshCw aria-hidden="true" />
            立即重读
          </Button>
        </div>
      </Notice>
    );
  }

  return (
    <p className="my-3 text-xs text-muted-foreground">
      最后观测 <code>{observed ?? '（尚无成功观测）'}</code>
      ：这是服务端生成该投影的时间，不是运行自身的进度。
    </p>
  );
}
