# 10. Schema migrations run at boot, for now

Status: accepted

## Context

The catalog owns one table. Its schema has to exist before it serves traffic.

## Decision

The catalog applies its schema idempotently at startup, before marking itself
started. The startup probe therefore covers migration, so a slow migration is
not mistaken for a hung process and restarted in a loop.

## Reasoning

For a schema this small and this additive, a boot-time migration removes a
moving part: no Job to sequence, no Helm hook ordering to get right, no separate
failure surface.

It does not generalise, and the boundary is worth stating. Boot-time migration
breaks down when:

- **The migration is not additive.** Several replicas boot concurrently during a
  rolling update. `CREATE TABLE IF NOT EXISTS` is safe under that; a destructive
  or reordering migration is not.
- **The migration is slow.** It blocks the startup probe for every replica, and
  a long `ALTER TABLE` will exceed the probe budget and produce a crash loop.
- **Old and new code must coexist.** A rolling update runs both versions against
  one schema, so any migration has to be compatible with both.

## Consequences

- One fewer component to operate today.
- The moment a non-additive migration is needed, it moves to a Helm
  `pre-upgrade` hook Job with the expand/contract pattern, and this ADR is
  superseded. `DB_MIGRATE_ON_BOOT=false` already exists to make that switch.
