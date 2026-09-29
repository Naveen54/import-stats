# importstats

Analyse the import graph of a React/JS/TS project — packages, sizes, cycles,
orphans, architecture rules — with an interactive dashboard.

```bash
npx importstats ./my-react-app
npx importstats -no-open -json report.json ./my-react-app
```

Flags go before the path. Use `-no-open` in headless/CI environments.
Run `npx importstats -h` for all options.

Source and docs: https://github.com/Naveen54/import-stats
