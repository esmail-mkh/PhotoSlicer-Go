# README screenshots

`screenshot.mjs` renders `assets/app-fa.jpg` and `assets/app-en.jpg` from the
real frontend in headless Chromium, so the pictures in the READMEs always show
the UI as it is. The Go backend is replaced by an empty stub, and the version
badge is read from `engine/constants/constants.go`.

The **Update README Screenshots** job in `.github/workflows/build-release.yml`
runs it on every workflow run and commits the images to the default branch
when they changed (release tags, and manual runs on the default branch). Other
manual runs only keep them as a workflow artifact.

Run it locally:

```sh
cd scripts/screenshot
npm ci
npx playwright install chromium
node screenshot.mjs          # writes ../../assets/app-fa.jpg and app-en.jpg
```

`SHOT_THEME` picks the colour: a built-in theme name (`blue`, `purple`, `ruby`,
`sunset`, `gold`, `emerald`) or any hex colour, which is applied like the custom
colour in Settings. The default is the deep red `#e60000`; try `#cc0000` for a
darker one or `#ff0000` for a brighter one. `SHOT_OUT` changes the output
folder. The output is deterministic, so an unchanged UI gives byte-identical
files and produces no commit.
