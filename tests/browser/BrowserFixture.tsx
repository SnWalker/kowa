import { useState } from 'react';
import { Button } from '../../src/ui/button';
import { ErrorBoundary } from '../../src/ui/ErrorBoundary';
import { LoadingState } from '../../src/ui/Feedback';

function FallibleContent() {
  const [broken, setBroken] = useState(false);
  if (broken) throw new Error('Deliberate browser acceptance failure');
  return <Button onClick={() => setBroken(true)}>触发渲染异常</Button>;
}

export function BrowserFixture() {
  return (
    <main className="p-6">
      <h1 className="mb-4 text-2xl font-semibold">基础组件验收</h1>
      <LoadingState />
      <div className="mt-4">
        <ErrorBoundary><FallibleContent /></ErrorBoundary>
      </div>
    </main>
  );
}
