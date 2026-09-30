import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { App } from './app/App';
import { ErrorBoundary } from './ui/ErrorBoundary';
import './styles/tokens.css';
import './styles/global.css';

const root = document.getElementById('root');
if (!root) throw new Error('Application root is missing');

createRoot(root).render(
  <StrictMode>
    <ErrorBoundary>
      <BrowserRouter><App /></BrowserRouter>
    </ErrorBoundary>
  </StrictMode>,
);
