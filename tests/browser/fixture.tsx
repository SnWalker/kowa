import { createRoot } from 'react-dom/client';
import { BrowserFixture } from './BrowserFixture';
import '../../src/styles/tokens.css';
import '../../src/styles/global.css';

const root = document.getElementById('root');
if (!root) throw new Error('Fixture root is missing');
createRoot(root).render(<BrowserFixture />);
