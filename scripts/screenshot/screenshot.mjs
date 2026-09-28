// Renders the README screenshots (assets/app-fa.jpg and assets/app-en.jpg)
// from the real frontend in headless Chromium, so they always show the UI as
// it is at this commit. The Go backend is replaced by a stub that answers
// every call with nothing; the page only needs it to exist.
//
//   cd scripts/screenshot && npm ci && npx playwright install chromium
//   node screenshot.mjs                # writes into ../../assets
//
// SHOT_OUT overrides the output folder. SHOT_THEME picks the colour theme: one
// of the built-in names (blue, purple, ruby, sunset, gold, emerald) or any hex
// colour such as #e60000, which is applied like the custom colour in Settings.
// The default is a deep, saturated red.
import { chromium } from 'playwright';
import { mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const outDir = process.env.SHOT_OUT || path.join(root, 'assets');
const theme = process.env.SHOT_THEME || '#e60000';

// The UI is drawn on a 520x810 canvas below a 40px title bar (DESIGN_WIDTH,
// DESIGN_HEIGHT and DESIGN_TITLEBAR in script.js), so this viewport renders it
// at exactly 100%, title bar included. Twice the pixel density keeps text sharp
// when GitHub scales the image down.
const viewport = { width: 520, height: 810 + 40 };
const variants = [
  { lang: 'fa', file: 'app-fa.jpg' },
  { lang: 'en', file: 'app-en.jpg' },
];

const constants = await readFile(path.join(root, 'engine', 'constants', 'constants.go'), 'utf8');
const version = /Version\s*=\s*"([^"]+)"/.exec(constants)?.[1];
if (!version) throw new Error('could not read Version from engine/constants/constants.go');

const frontend = pathToFileURL(path.join(root, 'frontend', 'index.html')).href;
await mkdir(outDir, { recursive: true });

const browser = await chromium.launch();
try {
  for (const { lang, file } of variants) {
    const context = await browser.newContext({
      viewport,
      deviceScaleFactor: 2,
      colorScheme: 'dark',
      // The stylesheet already collapses animations for this preference, which
      // also makes the render identical from run to run.
      reducedMotion: 'reduce',
    });
    const page = await context.newPage();
    const problems = [];
    page.on('pageerror', (err) => problems.push(err.message));

    await page.addInitScript((appVersion) => {
      window.__APP_VERSION__ = appVersion;
      window.go = { main: { App: new Proxy({}, { get: () => () => Promise.resolve() }) } };
    }, version);
    await page.goto(frontend);
    await page.evaluate(() => document.fonts.ready);

    await page.evaluate(({ lang, theme, version }) => {
      setLanguage(lang);
      if (theme.startsWith('#')) {
        setTheme('ruby'); // keeps the red dot marked as the selected theme
        applyCustomTheme(theme);
        document.querySelector('.dot-ruby')?.classList.add('active');
      } else {
        setTheme(theme);
      }
      applyAppVersion(version);
      showTab('process');
    }, { lang, theme, version });
    // The two background glows are absolutely positioned partly outside the
    // window. In a right-to-left page that makes the document 140px wider than
    // the viewport and Chromium then captures it shifted sideways. Pinning them
    // to the viewport keeps the same look without the extra overflow.
    await page.addStyleTag({ content: '.bg-orb { position: fixed !important; }' });
    await page.waitForTimeout(400);

    const target = path.join(outDir, file);
    await page.screenshot({ path: target, type: 'jpeg', quality: 90 });
    console.log(`${lang}: ${path.relative(root, target)} (v${version}, ${theme} theme)`);

    await context.close();
    if (problems.length) throw new Error(`page errors while rendering ${lang}:\n${problems.join('\n')}`);
  }
} finally {
  await browser.close();
}
