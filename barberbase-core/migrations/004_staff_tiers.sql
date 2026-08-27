-- 004_staff_tiers.sql
-- Barber experience tiers (Junior / Senior / Expert) and the per-tier pricing
-- they drive. Tier is an attribute of the barber; price is a function of
-- (variant, tier). Orthogonal to queue_routing_mode and requested_barber_id:
-- routing binds to a barber, pricing binds to a tier.
--
-- Schema only. Nothing reads or writes these tables yet — the price resolver
-- (T2), dispatch predicate (T3) and upgrade path (T4) come later.
--
-- No BEGIN;/COMMIT; — the migration runner owns the transaction. See README.md.

-- ---------------------------------------------------------------------------
-- staff_tiers
-- Per-location ladder. rank orders tiers against each other; use 10/20/30/40
-- spacing so a tier can be slotted between two existing ones without
-- renumbering the whole ladder (convention, not enforced by DDL — the only
-- rule is that ranks are unique within a location).
-- ---------------------------------------------------------------------------
CREATE TABLE staff_tiers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id),
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    name        VARCHAR(50) NOT NULL,
    rank        INT NOT NULL,
    description VARCHAR(160),
    is_default  BOOLEAN NOT NULL DEFAULT false,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(location_id, name),
    UNIQUE(location_id, rank)
);

-- One live default per location. Partial over is_active so a retired tier that
-- was once the default does not block naming a new one.
CREATE UNIQUE INDEX idx_staff_tiers_one_default
    ON staff_tiers(location_id) WHERE is_default AND is_active;

-- Which tier a barber sits in. NULL until the shop configures tiers; the price
-- resolver falls back to the variant's base price for untiered barbers.
-- No ON DELETE: a tier still referenced by a barber must not vanish silently.
ALTER TABLE staff_members ADD COLUMN tier_id UUID REFERENCES staff_tiers(id);

-- ---------------------------------------------------------------------------
-- service_variant_tier_prices
-- SPARSE overrides. An absent row means "inherit the variant's base
-- price_paise and duration_minutes" — do NOT backfill a full matrix. A
-- 40-variant, 4-tier shop should carry ~30 rows here, not 160.
--
-- duration_minutes NULL means inherit. price_paise is NOT NULL because an
-- override row exists precisely to state a price.
--
-- is_offered = false is how a tier is excluded from a service (juniors don't
-- do colour). That is a row saying "not offered", never a NULL price.
-- ---------------------------------------------------------------------------
CREATE TABLE service_variant_tier_prices (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id),
    location_id        UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    service_variant_id UUID NOT NULL REFERENCES service_variants(id) ON DELETE CASCADE,
    tier_id            UUID NOT NULL REFERENCES staff_tiers(id) ON DELETE CASCADE,
    price_paise        INT NOT NULL CHECK (price_paise >= 0),
    duration_minutes   INT CHECK (duration_minutes > 0),
    is_offered         BOOLEAN NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(service_variant_id, tier_id)
);

CREATE INDEX idx_tier_prices_variant
    ON service_variant_tier_prices(service_variant_id, tier_id);

-- The tier a waiting customer asked for, plus snapshots. The snapshots exist so
-- a barber's promotion mid-queue does not retroactively change what a waiting
-- customer chose. No ON DELETE on the FK for the same reason.
ALTER TABLE queue_entries
    ADD COLUMN required_tier_id   UUID REFERENCES staff_tiers(id),
    ADD COLUMN tier_name_snapshot VARCHAR(50),
    ADD COLUMN tier_rank_snapshot INT;

-- Tier on the charge line, plus append-plus-tombstone for tier upgrades:
-- a correction never mutates a row (Law 10), it stamps superseded_at on the old
-- one and inserts a replacement pointing back via supersedes_id. Same shape as
-- visit_payments.voided_at / corrected_by_payment_id in 001.
ALTER TABLE visit_services
    ADD COLUMN tier_id            UUID REFERENCES staff_tiers(id),
    ADD COLUMN tier_name_snapshot VARCHAR(50),
    ADD COLUMN superseded_at      TIMESTAMPTZ,
    ADD COLUMN supersedes_id      UUID REFERENCES visit_services(id);

-- The live charge lines for a visit. idx_visit_services_visit (001:744) stays:
-- it still serves queries that want superseded rows too, e.g. the audit trail.
CREATE INDEX idx_visit_services_active
    ON visit_services(visit_id) WHERE superseded_at IS NULL;

-- ---------------------------------------------------------------------------
-- Dispatch index: DELIBERATELY UNCHANGED.
--
-- An earlier draft of this unit dropped and recreated idx_queue_dispatch with
-- required_tier_id as the second key column, so T3's predicate would be
-- "covered". Measured at 50k rows, that is a regression:
--
--   (queue_session_id, priority_group, sort_key)                  -> Index Scan, no Sort
--   (queue_session_id, required_tier_id, priority_group, sort_key) -> Seq Scan + Sort
--
-- T3's predicate is "required_tier_id IS NULL OR required_tier_id = $n", a
-- disjunction rather than an equality, so the planner cannot treat that column
-- as part of the equality prefix. Placing it before the ORDER BY columns breaks
-- the ordered walk; once a Sort is forced, a sequential scan is cheaper and the
-- index is abandoned entirely.
--
-- The index from 001 already serves T3 optimally: ordered walk in dispatch
-- order, the tier predicate applied as a per-row Filter (one UUID comparison
-- against a value already in the tuple), LIMIT 1 stopping at the first match.
--
-- Appending required_tier_id last preserves the ordering but buys nothing — a
-- trailing column cannot be used for filtering during an ordered scan — while
-- adding 16 bytes per entry to the hottest index in the system.
--
-- Leave it alone. T3 needs no index work.
-- ---------------------------------------------------------------------------
