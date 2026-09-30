import { useState } from 'react';
import { ErrorBoundary } from '../../src/ui/ErrorBoundary';
import { LoadingState } from '../../src/ui/Feedback';

function FallibleContent() {
  const [broken, setBroken] = useState(false);
  if (broken) throw new Error('Deliberate browser acceptance failure');
  return <button onClick={() => setBroken(true)}>触发渲染异常</button>;
}

export function BrowserFixture() {
  return (
    <main>
      <h1>基础组件验收</h1>
      <LoadingState />
      <ErrorBoundary><FallibleContent /></ErrorBoundary>
    </main>
  );
}
