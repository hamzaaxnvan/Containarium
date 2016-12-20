import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'path';

// Vitest config for web-ui's unit tests (#2224). Scoped to this project only
// — the daemon's own Go test suite is unaffected and has its own gate.
export default defineConfig({
  plugins: [react()],
  resolve: {
