import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  server: { host: '127.0.0.1' },
  test: {
    environment: 'jsdom',
    include: ['tests/unit/**/*.test.tsx'],
    setupFiles: ['tests/unit/setup.ts'],
    restoreMocks: true,
  },
});
