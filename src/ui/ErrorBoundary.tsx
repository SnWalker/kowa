import { Component, createRef, type ReactNode } from 'react';

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
      <div className="feedback error-state" role="alert" tabIndex={-1} ref={this.alert}>
        <h2>内容暂时无法显示</h2>
        <p>可以重试，或返回首页继续浏览。</p>
        <div className="actions">
          <button onClick={() => this.setState({ failed: false })}>重试</button>
          <a href="/">返回首页</a>
        </div>
      </div>
    );
  }
}
