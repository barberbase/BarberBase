-- 003_media_and_catalog.sql
-- Shop/service/staff imagery (media_assets) and the platform-global style
-- taxonomy (catalog_style_templates), plus the columns linking them to
-- service_variants and locations.
--
-- Schema only. Nothing reads or writes these tables yet — the R2 upload pipeline
-- (M2) and catalog seeding (M3) come later. Landing the DDL now because it is
-- far harder to retrofit once visit_services carries live rows.
--
-- No BEGIN;/COMMIT; — the migration runner owns the transaction. See README.md.
-- No SET LOCAL statement_timeout: every statement here is metadata-only against
-- empty or tiny tables, well inside the 5s ceiling.

-- ---------------------------------------------------------------------------
-- media_assets
-- One row per uploaded image. r2_key is the object key in R2; content_hash lets
-- a re-upload of the same bytes be recognised rather than duplicated.
--
-- Lifecycle: 'pending' on presign, 'ready' once the upload is confirmed,
-- 'archived' on replacement. Only 'ready' rows are ever rendered, which is why
-- every uniqueness rule below is a partial index over status = 'ready' — a
-- replacement can be uploaded and confirmed without colliding with the asset it
-- is about to supersede.
--
-- purpose drives which optional FK must be set; the two CHECKs make the pairing
-- total in both directions, so a service_ref can never be orphaned from its
-- variant and a logo can never smuggle one in.
-- ---------------------------------------------------------------------------
CREATE TABLE media_assets (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id),
    location_id        UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    purpose            VARCHAR(20) NOT NULL
                       CHECK (purpose IN ('service_ref','location_logo',
                                          'location_cover','staff_avatar')),
    service_variant_id UUID REFERENCES service_variants(id) ON DELETE CASCADE,
    staff_member_id    UUID REFERENCES staff_members(id) ON DELETE CASCADE,
    r2_key             TEXT NOT NULL UNIQUE,
    content_hash       TEXT NOT NULL,
    width_px           INT,
    height_px          INT,
    bytes              INT,
    alt_text           VARCHAR(160),
    sort_order         INT NOT NULL DEFAULT 0,
    is_primary         BOOLEAN NOT NULL DEFAULT false,
    status             VARCHAR(20) NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','ready','archived')),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    committed_at       TIMESTAMPTZ,
    archived_at        TIMESTAMPTZ,
    CHECK ((purpose = 'service_ref')  = (service_variant_id IS NOT NULL)),
    CHECK ((purpose = 'staff_avatar') = (staff_member_id IS NOT NULL))
);

-- At most one live primary image per service variant.
CREATE UNIQUE INDEX idx_media_assets_one_primary_service
    ON media_assets(service_variant_id)
    WHERE purpose = 'service_ref' AND is_primary AND status = 'ready';

-- At most one live logo per location. (location_cover is deliberately
-- unconstrained — a location may show several.)
CREATE UNIQUE INDEX idx_media_assets_one_logo
    ON media_assets(location_id)
    WHERE purpose = 'location_logo' AND status = 'ready';

-- At most one live avatar per staff member.
CREATE UNIQUE INDEX idx_media_assets_one_avatar
    ON media_assets(staff_member_id)
    WHERE purpose = 'staff_avatar' AND status = 'ready';

-- The same bytes may not be attached twice to one variant, in any status.
CREATE UNIQUE INDEX idx_media_assets_variant_hash
    ON media_assets(service_variant_id, content_hash)
    WHERE purpose = 'service_ref';

-- Render path: a variant's live images in display order.
CREATE INDEX idx_media_assets_variant
    ON media_assets(service_variant_id, sort_order)
    WHERE status = 'ready';

-- Reaper: presigned uploads that were never confirmed.
CREATE INDEX idx_media_assets_reap
    ON media_assets(created_at) WHERE status = 'pending';

-- ---------------------------------------------------------------------------
-- catalog_style_templates
-- Platform-global style taxonomy with reference imagery, offered to shops during
-- onboarding. Deliberately has NO tenant_id: it belongs to the platform, not to
-- a shop. A nullable tenant_id would break Law 11 and poison every query with an
-- "OR tenant_id IS NULL".
--
-- Deliberately has NO price: taxonomy and imagery only. Price has one source of
-- truth per shop, on service_variants.
--
-- gender mirrors the service_categories CHECK at 001:293-294 exactly.
-- ---------------------------------------------------------------------------
CREATE TABLE catalog_style_templates (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_name            VARCHAR(100) NOT NULL,
    group_name               VARCHAR(100) NOT NULL,
    variant_name             VARCHAR(100) NOT NULL,
    gender                   VARCHAR(10) NOT NULL DEFAULT 'unisex'
                             CHECK (gender IN ('men','women','unisex')),
    default_duration_minutes INT NOT NULL CHECK (default_duration_minutes > 0),
    r2_key                   TEXT,
    alt_text                 VARCHAR(160),
    sort_order               INT NOT NULL DEFAULT 0,
    is_active                BOOLEAN NOT NULL DEFAULT true,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(category_name, group_name, variant_name, gender)
);

-- Which catalog template a shop's variant was created from, if any. NULL for
-- hand-written services.
ALTER TABLE service_variants
    ADD COLUMN template_id UUID REFERENCES catalog_style_templates(id);

-- logo_key is a denormalised snapshot alongside logo_asset_id, the same pattern
-- as the visit_services charge snapshots: a deleted asset nulls the FK but the
-- key survives, so a rendering page degrades instead of breaking.
ALTER TABLE locations
    ADD COLUMN logo_asset_id UUID REFERENCES media_assets(id) ON DELETE SET NULL,
    ADD COLUMN logo_key      TEXT,
    ADD COLUMN brand_hex     CHAR(7);
