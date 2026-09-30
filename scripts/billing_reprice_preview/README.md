# Billing reprice preview

This is a read-only tool. It loads today's pricing templates and ratios,
recalculates settled historical bills, and creates an Excel workbook. It does
not contain database write statements.

```sh
chmod +x scripts/billing_reprice_preview/run.sh

# By default: complete Beijing-time days 2026-07-15 and 2026-07-16
./scripts/billing_reprice_preview/run.sh

# Explicit affected interval and optional filters
./scripts/billing_reprice_preview/run.sh \
  --start 1784044800 --before 1784131200 \
  --channel 9 --model doubao-seed-2-0-lite-260428
```

Default range: `[2026-07-15 00:00:00, 2026-07-17 00:00:00)` in
`Asia/Shanghai`. Explicit `--start` and `--before` values override it.

Output names include the generation timestamp, for example:
`billing-reprice-output/reprice-preview-20260716-183045.xlsx`.

The workbook contains summary, repriced rows, skipped rows, and a current
pricing configuration snapshot. Rows requiring unavailable historical request
headers/parameters are skipped instead of estimated.

Summary sheets use Beijing calendar days. Model, user, and channel summaries
also retain the date dimension instead of combining different days.

Token repricing includes prompt/completion, cache read, cache creation (generic,
5-minute and 1-hour), image, and audio input/output dimensions when those
details exist in `billing_ledger.other`. Tiered expressions receive the same
normalized token variables (`p`, `c`, `cr`, `cc`, `cc1h`, `img`, `img_o`,
`ai`, `ao`) used by live settlement.

The script defaults to `https://goproxy.cn,direct` when downloading Go
modules. Set `GOPROXY` explicitly if the server uses another proxy.
