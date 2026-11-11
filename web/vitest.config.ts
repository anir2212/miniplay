import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: { environment: 'jsdom', include: ['src/admin/**/*.test.{ts,tsx}'], restoreMocks: true },
});
