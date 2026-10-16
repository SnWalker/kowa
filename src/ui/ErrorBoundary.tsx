import { OctagonAlert, RotateCcw } from 'lucide-react';
import { Component, createRef, type ReactNode } from 'react';
import { Button } from './button';

/**
 * Render failures are contained per route: the alert takes focus, the internal
 * exception detail never reaches the screen, and retrying returns focus to the
 * recovered content so the next Tab continues inside the page.
 */
export class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  private alert = createRef<HTMLDivElement>();
  private content = createRef<HTMLDivElement>();

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidCatch() {
    this.alert.current?.focus();
  }

  componentDidUpdate(_previousProps: { children: ReactNode }, previousState: { failed: boolean }) {
    if (previousState.failed && !this.state.failed) this.content.current?.focus();
  }

  render() {
    if (!this.state.failed) {
      return <div ref={this.content} tabIndex={-1}>{this.props.children}</div>;
    }
    return (
      <div
        className="rounded-md border border-danger-border bg-danger-muted px-4 py-4 break-anywhere"
        role="alert"
        tabIndex={-1}
        ref={this.alert}
      >
        <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
          <OctagonAlert aria-hidden="true" className="size-4 text-danger" />
          内容暂时无法显示
        </h2>
        <p className="mt-1 text-sm text-foreground">可以重试，或返回首页继续浏览。</p>
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <Button variant="secondary" onClick={() => this.setState({ failed: false })}>
            <RotateCcw aria-hidden="true" />
            重试
          </Button>
          <a href="/">返回首页</a>
        </div>
      </div>
    );
  }
}
