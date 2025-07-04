#!/usr/bin/env node

// Playwright and its child workers force colored output in some modes. Keeping
// NO_COLOR in the inherited env makes Node emit noisy warnings before tests run.
delete process.env.NO_COLOR

require('@playwright/test/cli')
