# Billing CNY Persistence

Overseas USD billing ledgers persist a CNY reference at final settlement when
`BILLING_CNY_REFERENCE_ENABLED=true`:

- `exchange_rate` is the cached latest provider rate available at settlement.
- `amount_cny_reference` is the USD ledger amount multiplied by that rate,
  rounded to 8 decimal places.
- The original rate source and capture timestamps are stored in
  `other.cny_reference`.
- Pending and finalizing ledgers do not freeze a rate.
- Domestic-origin bills never freeze a USD/CNY reference through this path.
- Fallback or stale rates are not persisted. If no fresh provider snapshot is
  available, the CNY fields remain empty instead of storing an estimated rate.
- Internal overseas-to-domestic imports carry the source USD amount, rate, and
  CNY reference without recalculating them on the destination node.

The user-facing read-only endpoint is:

```text
GET /api/billing?billing_id=<billing_id>
Authorization: Bearer sk-<token>
```

It only returns a ledger owned by the authenticated user. It returns the
persisted USD amount, exchange rate, and CNY amount without recalculating them
at query time, and it does not return upstream response bodies. The existing
`/api/billing_query` endpoint remains the audit endpoint and still supports its
existing compatibility behavior.

## Legacy Backfill

The `/billing-cny-backfill` binary previews by default. Use the same rate for
the entire batch. Take a database backup before applying:

```bash
docker exec -i postgres pg_dump -U root -d new-api -Fc > billing-before-cny-backfill.dump

docker exec new-api \
  /billing-cny-backfill \
  --server-region overseas \
  --before <unix_cutoff> \
  --batch-id <batch_id>
```

Review `matched`, `old_amount_usd`, and `backfilled_amount_cny`, then apply
only with the exact confirmation batch id:

```bash
docker exec new-api \
  /billing-cny-backfill \
  --server-region overseas \
  --before <unix_cutoff> \
  --batch-id <batch_id> \
  --confirm-batch <batch_id> \
  --apply
```

If the provider is unavailable, pass an explicitly reviewed rate with
`--rate <usd_to_cny>`. The apply operation writes one provenance record per
ledger with `policy=backfill_current_rate`, marks the batch, and resets the
corresponding billing sync outboxes to `pending`.
