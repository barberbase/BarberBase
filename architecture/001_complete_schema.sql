-- 001_complete_schema.sql
-- =============================================================================
-- BarberBase — Complete Phase 1 Schema
-- PostgreSQL 16
-- Supersedes: 001_core_queue_and_financials.sql
--
-- Design rules:
--   - All IDs are UUID v7 (gen_random_uuid() placeholder until pg_uuidv7 ext).
--     Swap DEFAULT gen_random_uuid() → gen_uuid_v7() once extension is loaded.
--   - All monetary values in PAISE (INT). Never NUMERIC/FLOAT for money.
--   - All timestamps in TIMESTAMPTZ (Asia/Kolkata converted at app layer).
--   - tenant_id on every tenant-owned table. App layer injects from JWT.
--   - No RLS. Application-layer tenant isolation via pgx middleware.
--   - Phone number is the canonical customer identity (E.164 format).
--   - BSUID stored as supplementary identity only, never used for sending.
-- =============================================================================

BEGIN;

-- ---------------------------------------------------------------------------
-- EXTENSION: pgcrypto for gen_random_uuid()
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ===========================================================================
-- TIER 1: TENANT PRIMITIVES
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- tenants
-- One row per business (CodeNXT Lab client).
-- Capability limits stored here directly (no plans table in Phase 1).
-- Billing managed via Google Sheet + manual Razorpay links until 50 shops.
-- ---------------------------------------------------------------------------
CREATE TABLE tenants (
    id                          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                        VARCHAR(255) NOT NULL,
    slug                        VARCHAR(100) UNIQUE NOT NULL,
                                -- URL-safe, e.g. "star-salon"
    owner_phone_number          VARCHAR(20) NOT NULL,
                                -- WhatsApp number for weekly summary + auth
                                -- E.164 format: +919876543210

    -- Capability limits (replace plan tables until 50+ shops)
    monthly_marketing_quota     INT         NOT NULL DEFAULT 100,
    monthly_transactional_quota INT         NOT NULL DEFAULT 1000,
    max_staff_members           INT         NOT NULL DEFAULT 5,
    max_locations               INT         NOT NULL DEFAULT 1,

    -- Lifecycle
    is_active                   BOOLEAN     NOT NULL DEFAULT true,
    suspended_at                TIMESTAMPTZ,
    suspension_reason           TEXT,
    trial_ends_at               TIMESTAMPTZ,

    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenants_slug ON tenants(slug);
CREATE INDEX idx_tenants_owner_phone ON tenants(owner_phone_number);

-- ---------------------------------------------------------------------------
-- locations
-- One tenant can have multiple locations (branches).
-- All queue state is location-scoped. Never cross-location.
-- ---------------------------------------------------------------------------
CREATE TABLE locations (
    id                              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                       UUID        NOT NULL REFERENCES tenants(id),
    slug                            VARCHAR(100) UNIQUE NOT NULL,
                                    -- Full slug: "star-salon/koramangala"
    name                            VARCHAR(255) NOT NULL,
    address                         TEXT,
    timezone                        VARCHAR(50)  NOT NULL DEFAULT 'Asia/Kolkata',

    -- Operating modes
    operation_mode                  VARCHAR(30)  NOT NULL DEFAULT 'hybrid'
                                    CHECK (operation_mode IN (
                                        'walk_in_only',
                                        'appointment_only',
                                        'hybrid'
                                    )),
    queue_routing_mode              VARCHAR(30)  NOT NULL DEFAULT 'pooled'
                                    CHECK (queue_routing_mode IN (
                                        'pooled',       -- any barber takes next
                                        'hybrid',       -- customer chooses any or specific
                                        'barber_specific' -- must choose barber
                                    )),
    service_display_mode            VARCHAR(20)  NOT NULL DEFAULT 'hierarchical'
                                    CHECK (service_display_mode IN (
                                        'flat',         -- <= 10 services, simple shops
                                        'grouped',      -- groups without deep hierarchy
                                        'hierarchical'  -- full category > group > variant
                                    )),

    -- Queue config
    remote_queue_enabled            BOOLEAN      NOT NULL DEFAULT true,
    max_total_queue_size            INT          NOT NULL DEFAULT 50,
    max_remote_queue_size           INT          NOT NULL DEFAULT 30,
    notify_when_people_ahead        INT          NOT NULL DEFAULT 2,
    notify_when_wait_minutes        INT          NOT NULL DEFAULT 20,
    allow_overtime_minutes          INT          NOT NULL DEFAULT 15,
                                    -- How many minutes past closing a service can start
    appointment_grace_minutes       INT          NOT NULL DEFAULT 10,
    appointment_checkin_early_min   INT          NOT NULL DEFAULT 15,
    late_appointment_policy         VARCHAR(30)  NOT NULL DEFAULT 'staff_approval'
                                    CHECK (late_appointment_policy IN (
                                        'staff_approval',
                                        'downgrade_to_walkin',
                                        'cancel'
                                    )),

    -- Arrival verification
    arrival_pin_hash                TEXT,
                                    -- bcrypt hash of the static PIN
    arrival_pin_plain               VARCHAR(6),
                                    -- Plaintext shown only in staff dashboard
                                    -- NEVER sent to browser/customer
    gps_latitude                    DECIMAL(10,8),
    gps_longitude                   DECIMAL(11,8),
    arrival_radius_metres           INT          NOT NULL DEFAULT 100,
    geolocation_assist              BOOLEAN      NOT NULL DEFAULT true,
    nfc_enabled                     BOOLEAN      NOT NULL DEFAULT false,
    nfc_token_hash                  TEXT,
                                    -- HMAC of (location_id + weekly_nonce)
    nfc_token_rotated_at            TIMESTAMPTZ,

    -- Watchdog thresholds (all configurable per location)
    stale_called_warning_minutes    INT          NOT NULL DEFAULT 5,
    stale_called_critical_minutes   INT          NOT NULL DEFAULT 10,
    in_progress_warning_minutes     INT          NOT NULL DEFAULT 10,
                                    -- Added to estimated_duration
    in_progress_confirm_minutes     INT          NOT NULL DEFAULT 15,
    in_progress_critical_minutes    INT          NOT NULL DEFAULT 25,
    auto_snooze_enabled             BOOLEAN      NOT NULL DEFAULT true,

    -- WhatsApp (Bhejna) per-location config
    -- Mode A (default): platform env credentials used; these columns stay NULL.
    -- Mode B (premium): shop's own WABA; credentials AES-256-GCM encrypted, decrypted at runtime.
    whatsapp_mode                   VARCHAR(20)  NOT NULL DEFAULT 'shared'
                                    CHECK (whatsapp_mode IN ('shared', 'own_number')),
    business_whatsapp_number        VARCHAR(20),    -- Mode B: from_business_phone (E.164)
    bhejna_api_key_encrypted        TEXT,           -- Mode B: AES-256-GCM encrypted api_key
    bhejna_webhook_secret_encrypted TEXT,           -- Mode B: AES-256-GCM encrypted webhook_secret

    is_active                       BOOLEAN      NOT NULL DEFAULT true,
    created_at                      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at                      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_locations_tenant   ON locations(tenant_id);
CREATE INDEX idx_locations_slug     ON locations(slug);

-- ---------------------------------------------------------------------------
-- location_hours
-- Operating hours per day of week.
-- day_of_week: 0=Sunday ... 6=Saturday (matches Go time.Weekday).
-- ---------------------------------------------------------------------------
CREATE TABLE location_hours (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    location_id     UUID        NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    day_of_week     SMALLINT    NOT NULL CHECK (day_of_week BETWEEN 0 AND 6),
    is_open         BOOLEAN     NOT NULL DEFAULT true,
    opens_at        TIME,       -- NULL if is_open = false
    closes_at       TIME,       -- NULL if is_open = false

    UNIQUE(location_id, day_of_week)
);

-- ---------------------------------------------------------------------------
-- location_status_overrides
-- Manual shop status overrides (lunch break, early close, etc.)
-- Computed effective_status in Go: manual_override > scheduled_hours.
-- ---------------------------------------------------------------------------
CREATE TABLE location_status_overrides (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    location_id     UUID        NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    status          VARCHAR(30) NOT NULL
                    CHECK (status IN (
                        'open',
                        'closing_soon',
                        'temporarily_closed',
                        'closed'
                    )),
    reason          TEXT,
    set_by          UUID,
                                -- FK added after staff_members is created (see ALTER TABLE
                                -- ADD CONSTRAINT fk_override_set_by below). Inline REFERENCES
                                -- is not possible here: staff_members is defined later and the
                                -- referenced table must exist at column-definition time.
    starts_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ,
                                -- NULL = manual reopen required
    cleared_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_location_overrides_active
    ON location_status_overrides(location_id, starts_at)
    WHERE cleared_at IS NULL;

-- ===========================================================================
-- TIER 2: STAFF MEMBERS
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- staff_members
-- Barbers, managers, owners. Auth via WhatsApp OTP → JWT.
-- ---------------------------------------------------------------------------
CREATE TABLE staff_members (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    location_id     UUID        NOT NULL REFERENCES locations(id),
    name            VARCHAR(100) NOT NULL,
    phone_number    VARCHAR(20) UNIQUE,
                                -- E.164. Used for WhatsApp OTP login.
                                -- UNIQUE globally (one WhatsApp = one staff account)
    role            VARCHAR(20) NOT NULL DEFAULT 'barber'
                    CHECK (role IN ('owner', 'manager', 'barber')),
    status          VARCHAR(20) NOT NULL DEFAULT 'offline'
                    CHECK (status IN ('idle', 'cutting', 'break', 'offline')),
    is_active       BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Web Push Subscription (Staff PWA)
    -- One subscription per staff member. Latest subscription wins on re-register.
    push_endpoint   TEXT,
                    -- FCM/APNs endpoint URL from W3C PushSubscription.
                    -- NULL when not subscribed or after FCM 410 Gone cleanup.
    push_p256dh     TEXT,
                    -- Base64url client EC public key for payload encryption.
    push_auth       TEXT,
                    -- Base64url 16-byte auth secret for payload encryption.
    push_enabled    BOOLEAN     NOT NULL DEFAULT false
                    -- false until staff grants push permission in /dashboard.
                    -- Set true by POST /v1/staff/push/subscribe.
                    -- Set false by outbox dispatch handler on FCM 410 Gone.
);

CREATE INDEX idx_staff_location   ON staff_members(tenant_id, location_id) WHERE is_active = true;
CREATE INDEX idx_staff_phone      ON staff_members(phone_number) WHERE phone_number IS NOT NULL;

-- Now that staff_members exists, add the FK we deferred above
ALTER TABLE location_status_overrides
    ADD CONSTRAINT fk_override_set_by
    FOREIGN KEY (set_by) REFERENCES staff_members(id);

-- ---------------------------------------------------------------------------
-- staff_otps
-- Short-lived WhatsApp login OTPs. PostgreSQL (not in-memory/SQLite): durable across
-- restarts and shared across nodes for future horizontal scaling — an OTP requested on
-- one node must verify on another. Keyed by phone_number (login precedes tenant context).
-- Self-cleaning: issuing a new OTP deletes prior rows for that phone (no sweep job).
-- ---------------------------------------------------------------------------
CREATE TABLE staff_otps (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_number    VARCHAR(20) NOT NULL,   -- E.164. Not an FK: verified before a session exists.
    otp_hash        TEXT        NOT NULL,   -- bcrypt hash of the 6-digit crypto/rand code
    attempts        INT         NOT NULL DEFAULT 0,  -- verify attempts; invalidated after 5
    consumed_at     TIMESTAMPTZ,            -- set on first successful verify; single-use
    expires_at      TIMESTAMPTZ NOT NULL,   -- created_at + 5 minutes
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_staff_otps_phone
    ON staff_otps(phone_number, created_at DESC);

-- ===========================================================================
-- TIER 3: SERVICE CATALOG (3-level hierarchy)
-- ===========================================================================
-- service_categories → service_groups → service_variants
--
-- service_variant is the BOOKABLE UNIT. Has price + duration.
-- service_group is a navigation container (e.g. "Fade", "Hair Color").
-- service_category is the top-level tab (e.g. "Hair", "Beard", "Skin").
--
-- visit_services snapshots variant data at booking time. Immutable.
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- service_categories
-- Top-level navigation. Men / Women / Unisex tabs on the web selector.
-- ---------------------------------------------------------------------------
CREATE TABLE service_categories (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    location_id     UUID        NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    name            VARCHAR(100) NOT NULL,   -- "Hair", "Beard", "Skin", "Nail", "Makeup"
    gender          VARCHAR(10)  NOT NULL DEFAULT 'unisex'
                    CHECK (gender IN ('men', 'women', 'unisex')),
    sort_order      INT         NOT NULL DEFAULT 0,
    is_active       BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(location_id, name, gender)
);

CREATE INDEX idx_service_categories_location
    ON service_categories(tenant_id, location_id)
    WHERE is_active = true;

-- ---------------------------------------------------------------------------
-- service_groups
-- Mid-level grouping. "Fade" contains Low/Mid/High/Skin/Taper variants.
-- ---------------------------------------------------------------------------
CREATE TABLE service_groups (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    location_id     UUID        NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    category_id     UUID        NOT NULL REFERENCES service_categories(id) ON DELETE CASCADE,
    name            VARCHAR(100) NOT NULL,   -- "Fade", "Hair Color", "Threading"
    description     TEXT,
    sort_order      INT         NOT NULL DEFAULT 0,
    is_active       BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(location_id, category_id, name)
);

CREATE INDEX idx_service_groups_category
    ON service_groups(tenant_id, location_id, category_id)
    WHERE is_active = true;

-- ---------------------------------------------------------------------------
-- service_variants
-- The bookable unit. Has price, duration, booking rules.
-- This replaces the old flat `services` table entirely.
-- ---------------------------------------------------------------------------
CREATE TABLE service_variants (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id),
    location_id             UUID        NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    group_id                UUID        NOT NULL REFERENCES service_groups(id) ON DELETE CASCADE,

    name                    VARCHAR(100) NOT NULL,  -- "Mid Fade", "Low Fade", "Skin Fade"
    description             TEXT,
    duration_minutes        INT         NOT NULL CHECK (duration_minutes > 0),
    buffer_minutes          INT         NOT NULL DEFAULT 0,
                            -- Added after service, not shown to customer
    price_paise             INT         NOT NULL CHECK (price_paise >= 0),

    -- Booking rules
    allow_walk_in           BOOLEAN     NOT NULL DEFAULT true,
    allow_appointment       BOOLEAN     NOT NULL DEFAULT true,
    requires_appointment    BOOLEAN     NOT NULL DEFAULT false,

    -- Display
    is_popular              BOOLEAN     NOT NULL DEFAULT false,
                            -- Surfaced in highlights, quick-pick
    sort_order              INT         NOT NULL DEFAULT 0,
    is_active               BOOLEAN     NOT NULL DEFAULT true,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(location_id, group_id, name)
);

CREATE INDEX idx_service_variants_group
    ON service_variants(tenant_id, location_id, group_id)
    WHERE is_active = true;

CREATE INDEX idx_service_variants_popular
    ON service_variants(location_id, is_popular)
    WHERE is_active = true AND is_popular = true;

-- ---------------------------------------------------------------------------
-- products
-- Physical retail items sold at checkout (beard oil, wax, etc.).
-- Do NOT affect queue duration. Checkout-only.
-- ---------------------------------------------------------------------------
CREATE TABLE products (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    location_id     UUID        REFERENCES locations(id),
                                -- NULL = available at all locations
    name            VARCHAR(255) NOT NULL,
    sku             VARCHAR(100),
    price_paise     INT         NOT NULL CHECK (price_paise >= 0),
    is_active       BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_products_location
    ON products(tenant_id, location_id, is_active);

-- ===========================================================================
-- TIER 4: CUSTOMERS & IDENTITIES
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- customers
-- Canonical customer record scoped to a tenant.
-- phone_number is the primary identity and the sending address for Bhejna.
-- BSUID is supplementary, stored in customer_identities.
-- ---------------------------------------------------------------------------
CREATE TABLE customers (
    id                          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                   UUID        NOT NULL REFERENCES tenants(id),

    -- Primary identity
    phone_number                VARCHAR(20),
                                -- E.164. Canonical sending address.
                                -- NULL only for anonymous staff-created walk-ins.
    name                        VARCHAR(100),

    -- Shadow profile support
    is_shadow_profile           BOOLEAN     NOT NULL DEFAULT false,
                                -- true when created from BSUID with no phone
    merged_into_customer_id     UUID        REFERENCES customers(id),
                                -- Set on merge. Never hard-delete shadows.

    -- Retention data
    last_visit_at               TIMESTAMPTZ,
    visit_count                 INT         NOT NULL DEFAULT 0,
    lifetime_value_paise        BIGINT      NOT NULL DEFAULT 0,

    -- Marketing consent
    marketing_opt_in            BOOLEAN     NOT NULL DEFAULT false,
    marketing_opt_in_at         TIMESTAMPTZ,
    marketing_opt_out_at        TIMESTAMPTZ,
    last_marketing_sent_at      TIMESTAMPTZ,

    -- Customer preferences (freeform, shown on barber dashboard)
    preferences                 JSONB       NOT NULL DEFAULT '{}',
                                -- e.g. {"haircut": "mid fade", "notes": "keep top long"}

    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, phone_number)
);

CREATE INDEX idx_customers_tenant_phone
    ON customers(tenant_id, phone_number)
    WHERE phone_number IS NOT NULL AND merged_into_customer_id IS NULL;

CREATE INDEX idx_customers_retention
    ON customers(tenant_id, last_visit_at, marketing_opt_in)
    WHERE merged_into_customer_id IS NULL;

-- ---------------------------------------------------------------------------
-- customer_identities
-- Supplementary identity providers per customer.
-- provider = 'whatsapp', provider_id = BSUID.
-- Phone number lookups use customers.phone_number directly.
-- ---------------------------------------------------------------------------
CREATE TABLE customer_identities (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id     UUID        NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    provider        VARCHAR(50) NOT NULL,   -- 'whatsapp'
    provider_id     VARCHAR(255) NOT NULL,  -- BSUID

    UNIQUE(provider, provider_id)
);

CREATE INDEX idx_customer_identities_lookup
    ON customer_identities(provider, provider_id);

-- ---------------------------------------------------------------------------
-- customer_notes
-- Staff-written notes per customer. Shown on barber dashboard at dispatch.
-- Separate from preferences JSONB for auditability and multiple note support.
-- ---------------------------------------------------------------------------
CREATE TABLE customer_notes (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    location_id     UUID        REFERENCES locations(id),
                                -- NULL = note visible across all locations
    customer_id     UUID        NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    note_type       VARCHAR(30) NOT NULL DEFAULT 'preference'
                    CHECK (note_type IN (
                        'preference',    -- "Mid fade, keep top long"
                        'service_note',  -- "Sensitive scalp, use gentle shampoo"
                        'manager_note',  -- Private to manager/owner
                        'warning'        -- "Has complained before"
                    )),
    note            TEXT        NOT NULL,
    visibility      VARCHAR(20) NOT NULL DEFAULT 'staff'
                    CHECK (visibility IN ('staff', 'manager_only')),
    created_by      UUID        REFERENCES staff_members(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX idx_customer_notes_lookup
    ON customer_notes(tenant_id, customer_id)
    WHERE deleted_at IS NULL;

-- ===========================================================================
-- TIER 5: QUEUE SESSIONS & CHECK-IN INTENTS
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- queue_sessions
-- One row per location per business date.
-- THE aggregate root. All queue mutations lock this row FOR UPDATE first.
-- queue_version: monotonically increasing counter. SSE clients compare this.
-- ---------------------------------------------------------------------------
CREATE TABLE queue_sessions (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES tenants(id),
    location_id         UUID        NOT NULL REFERENCES locations(id),
    business_date       DATE        NOT NULL,

    status              VARCHAR(20) NOT NULL DEFAULT 'active'
                        CHECK (status IN (
                            'active',   -- normal operation
                            'paused',   -- temporary closure
                            'ending',   -- shop closing, serving remaining
                            'closed',   -- done for the day
                            'archived'  -- cleaned up by end-of-day job
                        )),

    last_token_number   INT         NOT NULL DEFAULT 0,
                        -- Monotonically increasing. Never reused within a session.
    queue_version       INT         NOT NULL DEFAULT 0,
                        -- Incremented on every mutation. SSE ping carries this.

    opened_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at           TIMESTAMPTZ,
    archived_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(location_id, business_date)
);

CREATE INDEX idx_queue_sessions_location
    ON queue_sessions(tenant_id, location_id, business_date);

-- ---------------------------------------------------------------------------
-- checkin_intents
-- Created when customer scans QR or opens shop URL.
-- Bridges the gap between browser action and WhatsApp webhook arrival.
-- Solves the closing-time race: resolution uses status_at_creation, not now().
-- Expires in 23 hours (same as WhatsApp 24-hour free service window - 1h buffer).
-- ---------------------------------------------------------------------------
CREATE TABLE checkin_intents (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id),
    location_id             UUID        NOT NULL REFERENCES locations(id),

    token_code              VARCHAR(10) UNIQUE NOT NULL,
                            -- 6-char shortcode e.g. "JN8K4P"
                            -- Included in WhatsApp message body

    channel                 VARCHAR(20) NOT NULL DEFAULT 'whatsapp'
                            CHECK (channel IN (
                                'whatsapp',
                                'web_otp',      -- future SMS OTP path
                                'staff_created' -- staff manually creates
                            )),

    -- Snapshot of shop state at intent creation time
    -- Used in resolution decision even if shop closes before webhook arrives
    shop_status_at_creation VARCHAR(30) NOT NULL,

    -- Selected variant IDs carried through the intent
    -- JSON array of UUID strings
    variant_ids             JSONB       NOT NULL DEFAULT '[]',
    party_size              INT         NOT NULL DEFAULT 1,
    customer_name           VARCHAR(100),

    -- Lifecycle
    status                  VARCHAR(20) NOT NULL DEFAULT 'created'
                            CHECK (status IN (
                                'created',   -- waiting for WhatsApp message
                                'resolved',  -- queue_entry created
                                'expired',   -- past expires_at
                                'rejected'   -- shop closed/force-closed at resolution
                            )),
    source_ip               INET,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at              TIMESTAMPTZ NOT NULL
                            -- Law 13: MUST equal created_at + 23 hours. Set by application on INSERT.
                            -- GENERATED ALWAYS AS cannot be used here: timestamptz+interval is STABLE not IMMUTABLE in PG16.
);

CREATE INDEX idx_checkin_intents_token
    ON checkin_intents(token_code)
    WHERE status = 'created';

CREATE INDEX idx_checkin_intents_expiry
    ON checkin_intents(expires_at)
    WHERE status = 'created';

-- ===========================================================================
-- TIER 6: APPOINTMENTS
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- appointments
-- Scheduled bookings. Becomes a queue_entry when customer checks in on the day.
-- Not the same as a queue_entry. Appointment = intent. queue_entry = operational.
-- ---------------------------------------------------------------------------
CREATE TABLE appointments (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id),
    location_id             UUID        NOT NULL REFERENCES locations(id),
    customer_id             UUID        NOT NULL REFERENCES customers(id),
    requested_barber_id     UUID        REFERENCES staff_members(id),

    -- Selected services at booking time
    -- Variant IDs stored here; snapshotted into visit_services at check-in
    variant_ids             JSONB       NOT NULL DEFAULT '[]',
    party_size              INT         NOT NULL DEFAULT 1,
    total_duration_minutes  INT         NOT NULL,

    scheduled_start_at      TIMESTAMPTZ NOT NULL,
    scheduled_end_at        TIMESTAMPTZ NOT NULL,
                            -- MUST equal scheduled_start_at + duration. Set by application on INSERT.
                            -- GENERATED ALWAYS AS cannot be used here: timestamptz+interval is STABLE not IMMUTABLE in PG16.

    status                  VARCHAR(20) NOT NULL DEFAULT 'scheduled'
                            CHECK (status IN (
                                'scheduled',    -- future booking
                                'checked_in',   -- customer arrived, visit created
                                'cancelled',    -- cancelled before day
                                'no_show',      -- did not arrive
                                'rescheduled'   -- future: moved to new slot
                            )),

    -- Confirmation tracking
    confirmation_sent_at    TIMESTAMPTZ,
    reminder_sent_at        TIMESTAMPTZ,

    -- Cancellation audit
    cancelled_at            TIMESTAMPTZ,
    cancelled_by            VARCHAR(20) CHECK (cancelled_by IN ('customer', 'staff', 'system')),

    initiated_via           VARCHAR(20) NOT NULL DEFAULT 'web_form'
                            CHECK (initiated_via IN (
                                'whatsapp', 'web_form', 'staff_dashboard', 'ai_agent'
                            )),

    idempotency_key         VARCHAR(100) UNIQUE,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_appointments_location_date
    ON appointments(tenant_id, location_id, scheduled_start_at)
    WHERE status IN ('scheduled', 'checked_in');

CREATE INDEX idx_appointments_customer
    ON appointments(tenant_id, customer_id)
    WHERE status NOT IN ('cancelled', 'no_show');

-- ===========================================================================
-- TIER 7: VISITS — THE PARENT OPERATIONAL UNIT
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- visits
-- One customer's interaction with one location on one day.
-- Parent of visit_services and queue_entry.
-- walk_in, appointment, staff_created are the three entry types.
-- ---------------------------------------------------------------------------
CREATE TABLE visits (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id),
    location_id             UUID        NOT NULL REFERENCES locations(id),
    customer_id             UUID        REFERENCES customers(id),
    appointment_id          UUID        REFERENCES appointments(id),
                            -- Set only when entry_type = 'appointment'

    entry_type              VARCHAR(20) NOT NULL
                            CHECK (entry_type IN (
                                'walk_in',
                                'appointment',
                                'staff_created'
                            )),

    status                  VARCHAR(20) NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'completed', 'cancelled')),

    party_size              INT         NOT NULL DEFAULT 1
                            CHECK (party_size >= 1),
    total_duration_minutes  INT         NOT NULL CHECK (total_duration_minutes > 0),

    initiated_via           VARCHAR(20) NOT NULL DEFAULT 'whatsapp'
                            CHECK (initiated_via IN (
                                'whatsapp',
                                'web_form',
                                'staff_dashboard',
                                'ai_agent'
                            )),

    -- Magic link session
    -- HMAC-SHA256(customer_id + location_id + visit_id + expires_at, secret)
    magic_link_token_hash   TEXT,
    magic_link_expires_at   TIMESTAMPTZ,

    idempotency_key         VARCHAR(100) UNIQUE,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at            TIMESTAMPTZ,
    cancelled_at            TIMESTAMPTZ
);

CREATE INDEX idx_visits_location_date
    ON visits(tenant_id, location_id, created_at)
    WHERE status = 'active';

CREATE INDEX idx_visits_customer
    ON visits(tenant_id, customer_id, created_at)
    WHERE status != 'cancelled';

-- ---------------------------------------------------------------------------
-- visit_services
-- Immutable snapshot of selected service variants at booking time.
-- If shop changes prices next month, this visit's history is unaffected.
-- References service_variant_id for historical traceability.
-- ---------------------------------------------------------------------------
CREATE TABLE visit_services (
    id                          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    visit_id                    UUID        NOT NULL REFERENCES visits(id) ON DELETE CASCADE,
    service_variant_id          UUID        REFERENCES service_variants(id),
                                -- NULLABLE: variant may be deleted, snapshot still valid

    -- Immutable snapshots captured at booking time
    variant_name_snapshot       VARCHAR(100) NOT NULL,  -- "Mid Fade"
    group_name_snapshot         VARCHAR(100) NOT NULL,  -- "Fade"
    category_name_snapshot      VARCHAR(100) NOT NULL,  -- "Hair"
    duration_minutes_snapshot   INT         NOT NULL,
    price_paise_snapshot        INT         NOT NULL,

    sort_order                  INT         NOT NULL DEFAULT 0,

    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_visit_services_visit
    ON visit_services(visit_id);

-- ===========================================================================
-- TIER 8: QUEUE ENTRIES — THE OPERATIONAL TOKEN
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- queue_entries
-- One per visit in Phase 1. The pawn that moves through the state machine.
-- All mutations MUST first lock queue_sessions FOR UPDATE.
-- is_dispatchable: false when presence=snoozed or state=skipped/expired/etc.
-- ---------------------------------------------------------------------------
CREATE TABLE queue_entries (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    visit_id                UUID        NOT NULL REFERENCES visits(id) ON DELETE CASCADE,
    queue_session_id        UUID        NOT NULL REFERENCES queue_sessions(id),
    customer_id             UUID        REFERENCES customers(id),
                            -- Denormalized from visits.customer_id at entry creation time.
                            -- NULL for anonymous staff-created walk-ins.
                            -- Used by idx_queue_entries_one_active_per_customer to enforce
                            -- one active entry per known customer per session.

    token_number            INT         NOT NULL,
                            -- Assigned from queue_sessions.last_token_number++
                            -- Sequential within a session, never reused

    -- Operational state machine
    state                   VARCHAR(20) NOT NULL DEFAULT 'waiting'
                            CHECK (state IN (
                                'waiting',      -- in queue, not yet called
                                'called',       -- barber called them
                                'in_progress',  -- service started
                                'completed',    -- service done + checkout
                                'skipped',      -- called but absent, can return
                                'no_show',      -- terminal absent
                                'cancelled',    -- customer or staff cancelled
                                'expired',      -- end-of-day cleanup
                                'needs_review'  -- end-of-day: in_progress never checked out;
                                                -- not dispatchable, awaits manual reconciliation
                            )),

    -- Physical presence (separate from queue position)
    presence_state          VARCHAR(20) NOT NULL DEFAULT 'remote'
                            CHECK (presence_state IN (
                                'remote',       -- joined from outside, not present
                                'notified',     -- near-turn WhatsApp sent
                                'on_the_way',   -- customer self-confirmed via magic link
                                'arrived',      -- verified via PIN/GPS/NFC/staff
                                'snoozed',      -- remote, failed to engage at turn
                                'unknown'       -- anonymous walk-in, presence unclear
                            )),

    is_dispatchable         BOOLEAN     NOT NULL DEFAULT true,
                            -- false when snoozed, skipped, no_show, cancelled, expired
                            -- Barber "Call Next" only considers is_dispatchable = true

    -- Barber routing
    requested_barber_id     UUID        REFERENCES staff_members(id),
                            -- Customer preference. Respected in hybrid/barber_specific mode.
    assigned_barber_id      UUID        REFERENCES staff_members(id),
                            -- Actual barber assigned at call time.

    -- Dispatch ordering
    priority_group          INT         NOT NULL DEFAULT 100,
                            -- Lower = higher priority. Walk-ins: 100. Appointments: 50.
    sort_key                BIGINT      NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
                            -- Within priority_group, order by sort_key ASC.

    -- Session channel (determines notification path)
    session_channel         VARCHAR(20) NOT NULL DEFAULT 'whatsapp'
                            CHECK (session_channel IN ('whatsapp', 'web')),

    -- Stale state warnings (set by watchdog job, cleared on state transition)
    stale_warning           VARCHAR(30),
                            -- 'called_5min', 'called_10min', 'in_progress_overrun', etc.

    -- Timestamps (all nullable except remote_joined_at which = created_at)
    remote_joined_at        TIMESTAMPTZ,    -- When queue_entry was created (remote join)
    near_turn_notified_at   TIMESTAMPTZ,    -- When near-turn WhatsApp was sent
    on_the_way_at           TIMESTAMPTZ,    -- When customer tapped "On my way"
    arrived_at              TIMESTAMPTZ,    -- When physical arrival was verified
    snoozed_at              TIMESTAMPTZ,    -- When auto-snoozed by system
    reactivated_at          TIMESTAMPTZ,    -- When staff reactivated from snoozed
    called_at               TIMESTAMPTZ,    -- When barber tapped "Call Next"
    started_at              TIMESTAMPTZ,    -- When barber tapped "Start"
    completed_at            TIMESTAMPTZ,    -- When checkout completed

    UNIQUE(visit_id),
    UNIQUE(queue_session_id, token_number)
);

-- Primary dispatch index: what "Call Next" queries
CREATE INDEX idx_queue_dispatch
    ON queue_entries(queue_session_id, priority_group, sort_key)
    WHERE is_dispatchable = true AND state = 'waiting';

-- Watchdog query index: find stale called/in_progress entries
CREATE INDEX idx_queue_stale
    ON queue_entries(queue_session_id, state, called_at, started_at)
    WHERE state IN ('called', 'in_progress');

-- Presence tracking index
CREATE INDEX idx_queue_presence
    ON queue_entries(queue_session_id, presence_state)
    WHERE state IN ('waiting', 'called');

-- One active entry per known customer per session (prevents duplicate tokens on double-scan).
-- Anonymous walk-ins (customer_id IS NULL) are excluded — multiple are allowed.
-- Terminal states are excluded — a completed customer may re-enter the same day.
CREATE UNIQUE INDEX idx_queue_entries_one_active_per_customer
    ON queue_entries(queue_session_id, customer_id)
    WHERE state IN ('waiting', 'called', 'in_progress')
      AND customer_id IS NOT NULL;

-- Now add the FK from checkin_intents that references queue_entries
ALTER TABLE checkin_intents
    ADD COLUMN resolved_queue_entry_id UUID REFERENCES queue_entries(id);

-- ---------------------------------------------------------------------------
-- arrival_attempts
-- Audit log for PIN/GPS/NFC arrival verification attempts.
-- Used for rate limiting (max 5 per queue_entry, 10 per IP per hour).
-- ---------------------------------------------------------------------------
CREATE TABLE arrival_attempts (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    location_id     UUID        NOT NULL REFERENCES locations(id),
    queue_entry_id  UUID        NOT NULL REFERENCES queue_entries(id) ON DELETE CASCADE,
    method          VARCHAR(20) NOT NULL
                    CHECK (method IN ('pin', 'geolocation', 'nfc', 'staff')),
    success         BOOLEAN     NOT NULL,
    ip_address      INET,
    attempted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_arrival_attempts_entry
    ON arrival_attempts(queue_entry_id, attempted_at);

CREATE INDEX idx_arrival_attempts_ip_rate_limit
    ON arrival_attempts(ip_address, attempted_at);
    -- Time-window filter belongs in the query WHERE: NOW() is STABLE not IMMUTABLE
    -- and a NOW()-based index predicate aborts the migration.

-- ===========================================================================
-- TIER 9: FINANCIALS
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- visit_charges
-- One per visit. Created in draft at checkout start, finalized on completion.
-- UNIQUE(visit_id) enforces one bill per visit.
-- ---------------------------------------------------------------------------
CREATE TABLE visit_charges (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id),
    location_id             UUID        NOT NULL REFERENCES locations(id),
    visit_id                UUID        NOT NULL REFERENCES visits(id) ON DELETE CASCADE,

    subtotal_amount_paise   INT         NOT NULL DEFAULT 0 CHECK (subtotal_amount_paise >= 0),
    discount_amount_paise   INT         NOT NULL DEFAULT 0 CHECK (discount_amount_paise >= 0),
    total_amount_paise      INT         NOT NULL DEFAULT 0 CHECK (total_amount_paise >= 0),
    discount_reason         VARCHAR(100),

    status                  VARCHAR(20) NOT NULL DEFAULT 'draft'
                            CHECK (status IN ('draft', 'finalized', 'voided')),

    finalized_at            TIMESTAMPTZ,
    finalized_by            UUID        REFERENCES staff_members(id),
    voided_at               TIMESTAMPTZ,
    void_reason             TEXT,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(visit_id)
);

CREATE INDEX idx_visit_charges_location_date
    ON visit_charges(tenant_id, location_id, finalized_at)
    WHERE status = 'finalized';

-- ---------------------------------------------------------------------------
-- visit_charge_line_items
-- Individual line items on the bill: services rendered + products sold.
-- Services: come from visit_services (duration-affecting).
-- Products: beard oil, wax, etc. (checkout-only, no queue impact).
-- Discounts: applied as negative line items or via discount_amount_paise.
-- ---------------------------------------------------------------------------
CREATE TABLE visit_charge_line_items (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id),
    visit_charge_id         UUID        NOT NULL REFERENCES visit_charges(id) ON DELETE CASCADE,

    line_type               VARCHAR(20) NOT NULL
                            CHECK (line_type IN ('service', 'product', 'discount', 'manual')),

    -- Source references (nullable for manual/discount lines)
    service_variant_id      UUID        REFERENCES service_variants(id),
    product_id              UUID        REFERENCES products(id),

    -- Immutable snapshot
    name_snapshot           VARCHAR(255) NOT NULL,
    quantity                INT         NOT NULL DEFAULT 1 CHECK (quantity >= 1),
    unit_amount_paise       INT         NOT NULL,
    total_amount_paise      INT         NOT NULL,

    -- Staff who added the item (for commission attribution)
    staff_member_id         UUID        REFERENCES staff_members(id),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_line_items_charge
    ON visit_charge_line_items(visit_charge_id);

-- ---------------------------------------------------------------------------
-- visit_payments
-- One or more payment rows per visit_charge (supports split payment).
-- Sum of amount_paise across rows = visit_charges.total_amount_paise.
-- Never delete. Void via voided_at + corrected_by_payment_id.
-- ---------------------------------------------------------------------------
CREATE TABLE visit_payments (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id),
    location_id             UUID        NOT NULL REFERENCES locations(id),
    visit_charge_id         UUID        NOT NULL REFERENCES visit_charges(id) ON DELETE CASCADE,

    method                  VARCHAR(20) NOT NULL
                            CHECK (method IN (
                                'cash',
                                'upi',
                                'card',
                                'unpaid',
                                'complimentary'
                            )),
    amount_paise            INT         NOT NULL CHECK (amount_paise >= 0),
    provider_reference_id   VARCHAR(100),
                            -- UPI transaction ID, entered manually by staff

    -- Payment correction audit trail (never delete, always void + re-insert)
    voided_at               TIMESTAMPTZ,
    voided_by               UUID        REFERENCES staff_members(id),
    void_reason             TEXT,
    corrected_by_payment_id UUID        REFERENCES visit_payments(id),
                            -- Points to the replacement payment row

    collected_by            UUID        REFERENCES staff_members(id),
    collected_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payments_charge
    ON visit_payments(visit_charge_id)
    WHERE voided_at IS NULL;

CREATE INDEX idx_payments_location_date
    ON visit_payments(tenant_id, location_id, collected_at)
    WHERE voided_at IS NULL;

-- ---------------------------------------------------------------------------
-- staff_commission_rules
-- Per-staff commission configuration.
-- percentage_bps: basis points. 40% = 4000 bps. Avoids floating point.
-- ---------------------------------------------------------------------------
CREATE TABLE staff_commission_rules (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES tenants(id),
    location_id         UUID        NOT NULL REFERENCES locations(id),
    staff_member_id     UUID        NOT NULL REFERENCES staff_members(id),
    rule_type           VARCHAR(30) NOT NULL
                        CHECK (rule_type IN (
                            'percentage',
                            'fixed_per_service',
                            'none'
                        )),
    percentage_bps      INT         CHECK (percentage_bps BETWEEN 0 AND 10000),
                        -- Required when rule_type = percentage
    fixed_amount_paise  INT         CHECK (fixed_amount_paise >= 0),
                        -- Required when rule_type = fixed_per_service
    applies_to          VARCHAR(30) NOT NULL DEFAULT 'services_only'
                        CHECK (applies_to IN (
                            'services_only',
                            'products_only',
                            'services_and_products'
                        )),
    is_active           BOOLEAN     NOT NULL DEFAULT true,
    effective_from      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_until     TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_commission_rules_staff
    ON staff_commission_rules(tenant_id, staff_member_id)
    WHERE is_active = true;

-- ---------------------------------------------------------------------------
-- staff_commission_ledger
-- Immutable ledger rows written at checkout completion.
-- Snapshotted rule ensures historical accuracy even if rule changes.
-- Phase 1: used for daily hisab query. Phase 2: full settlement workflow.
-- ---------------------------------------------------------------------------
CREATE TABLE staff_commission_ledger (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id),
    location_id             UUID        NOT NULL REFERENCES locations(id),
    staff_member_id         UUID        NOT NULL REFERENCES staff_members(id),
    visit_charge_id         UUID        REFERENCES visit_charges(id),
    visit_charge_line_id    UUID        REFERENCES visit_charge_line_items(id),

    ledger_type             VARCHAR(20) NOT NULL DEFAULT 'commission_earned'
                            CHECK (ledger_type IN (
                                'commission_earned',
                                'adjustment',
                                'reversal'
                            )),
    source_type             VARCHAR(20) NOT NULL
                            CHECK (source_type IN ('service', 'product', 'manual_adjustment')),

    gross_amount_paise      INT         NOT NULL,
    commission_rate_bps     INT,
    commission_amount_paise INT         NOT NULL,
    currency                CHAR(3)     NOT NULL DEFAULT 'INR',

    rule_snapshot           JSONB       NOT NULL DEFAULT '{}',
                            -- Captures rule_type, rate, applies_to at time of calculation

    settlement_status       VARCHAR(20) NOT NULL DEFAULT 'unsettled'
                            CHECK (settlement_status IN ('unsettled', 'settled', 'voided')),
    settlement_id           UUID,       -- FK to staff_settlements (Phase 2)

    notes                   TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by              UUID        REFERENCES staff_members(id)
);

CREATE INDEX idx_commission_ledger_staff_date
    ON staff_commission_ledger(tenant_id, location_id, staff_member_id, created_at)
    WHERE settlement_status = 'unsettled';

-- ===========================================================================
-- TIER 10: FEEDBACK
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- feedback_requests
-- Created by outbox worker 30 minutes after visit completed.
-- Bhejna sends WhatsApp message. Response comes back via webhook.
-- ---------------------------------------------------------------------------
CREATE TABLE feedback_requests (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES tenants(id),
    location_id         UUID        NOT NULL REFERENCES locations(id),
    visit_id            UUID        NOT NULL REFERENCES visits(id) ON DELETE CASCADE,
    customer_id         UUID        REFERENCES customers(id),
    staff_member_id     UUID        REFERENCES staff_members(id),
                        -- Barber who performed the service

    channel             VARCHAR(20) NOT NULL DEFAULT 'whatsapp'
                        CHECK (channel IN ('whatsapp', 'web')),

    status              VARCHAR(20) NOT NULL DEFAULT 'scheduled'
                        CHECK (status IN (
                            'scheduled',  -- in outbox, not yet sent
                            'sent',       -- dispatched via Bhejna
                            'responded',  -- customer replied
                            'expired',    -- no reply within window
                            'failed',     -- Bhejna send failed
                            'cancelled'   -- visit cancelled before send
                        )),

    scheduled_at        TIMESTAMPTZ NOT NULL,
                        -- completed_at + 30 minutes
    sent_at             TIMESTAMPTZ,
    expires_at          TIMESTAMPTZ,
                        -- sent_at + 23 hours (within WhatsApp free window)

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, visit_id, channel)
);

CREATE INDEX idx_feedback_requests_scheduled
    ON feedback_requests(scheduled_at)
    WHERE status = 'scheduled';

-- ---------------------------------------------------------------------------
-- feedback_responses
-- One per visit. Unique constraint prevents duplicate submissions.
-- Low ratings surface in barber analytics view (no manager_alerts in Phase 1).
-- ---------------------------------------------------------------------------
CREATE TABLE feedback_responses (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id),
    location_id             UUID        NOT NULL REFERENCES locations(id),
    feedback_request_id     UUID        NOT NULL REFERENCES feedback_requests(id),
    visit_id                UUID        NOT NULL REFERENCES visits(id),
    customer_id             UUID        REFERENCES customers(id),
    staff_member_id         UUID        REFERENCES staff_members(id),

    rating                  INT         NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment                 TEXT,
    source                  VARCHAR(20) NOT NULL DEFAULT 'whatsapp'
                            CHECK (source IN ('whatsapp', 'web')),
    is_late                 BOOLEAN     NOT NULL DEFAULT false,
                            -- true if received after request expired

    received_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, feedback_request_id)
);

CREATE INDEX idx_feedback_responses_staff
    ON feedback_responses(tenant_id, location_id, staff_member_id, received_at);

CREATE INDEX idx_feedback_responses_rating
    ON feedback_responses(tenant_id, location_id, rating)
    WHERE rating <= 2;

-- ===========================================================================
-- TIER 11: NOTIFICATIONS & QUOTAS
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- notification_events
-- Every outbound Bhejna call logged here.
-- Connects quota usage, outbox events, and Bhejna provider IDs.
-- ---------------------------------------------------------------------------
CREATE TABLE notification_events (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES tenants(id),
    location_id         UUID        REFERENCES locations(id),
    customer_id         UUID        REFERENCES customers(id),

    channel             VARCHAR(20) NOT NULL DEFAULT 'whatsapp'
                        CHECK (channel IN ('whatsapp', 'sms', 'web_push')),
                        -- 'web_push' rows: customer_id=NULL, recipient_phone=NULL,
                        -- source_type='staff_member', source_id=staff_member_id.
                        -- status only ever reaches 'sent' (no FCM delivery receipt).
                        -- quota_type=NULL (web_push bypasses Bhejna quota).
    notification_type   VARCHAR(50) NOT NULL,
                        -- WhatsApp: 'queue_joined', 'near_turn', 'you_are_next',
                        --           'feedback_request', 'appointment_confirmed',
                        --           'appointment_reminder', 'marketing_broadcast',
                        --           'staff_otp', 'weekly_summary'
                        -- Web push: 'push_call_next'
    quota_type          VARCHAR(30),
                        -- 'whatsapp_transactional' or 'whatsapp_marketing'

    recipient_phone     VARCHAR(20),
    template_code       VARCHAR(100),

    status              VARCHAR(30) NOT NULL DEFAULT 'queued'
                        CHECK (status IN (
                            'queued',
                            'sent',
                            'delivered',
                            'failed',
                            'blocked_quota',
                            'skipped_opt_out'
                        )),

    -- Bhejna response data
    provider_message_id VARCHAR(255),
    error_message       TEXT,

    -- Source reference (which queue_entry, visit, or campaign triggered this)
    source_type         VARCHAR(30),
    source_id           UUID,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at             TIMESTAMPTZ,
    delivered_at        TIMESTAMPTZ
);

CREATE INDEX idx_notification_events_customer
    ON notification_events(tenant_id, customer_id, created_at);

CREATE INDEX idx_notification_events_status
    ON notification_events(tenant_id, status, created_at)
    WHERE status IN ('queued', 'failed');

-- ---------------------------------------------------------------------------
-- tenant_quota_periods
-- Monthly quota buckets per tenant per quota type.
-- Marketing and transactional are STRICTLY SEPARATE buckets.
-- Marketing quota exhaustion NEVER blocks queue notifications.
-- ---------------------------------------------------------------------------
CREATE TABLE tenant_quota_periods (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    quota_type      VARCHAR(30) NOT NULL
                    CHECK (quota_type IN (
                        'whatsapp_transactional',
                        'whatsapp_marketing',
                        'sms_otp'
                    )),
    period_start    DATE        NOT NULL,
    period_end      DATE        NOT NULL,
    included_limit  INT         NOT NULL,
    used_count      INT         NOT NULL DEFAULT 0,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, quota_type, period_start)
);

CREATE INDEX idx_quota_periods_lookup
    ON tenant_quota_periods(tenant_id, quota_type, period_start);

-- ---------------------------------------------------------------------------
-- quota_usage_ledger
-- Append-only log of every quota consumption event.
-- idempotency_key prevents double-counting on retry.
-- Used for audit and billing reconciliation.
-- ---------------------------------------------------------------------------
CREATE TABLE quota_usage_ledger (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES tenants(id),
    quota_type          VARCHAR(30) NOT NULL,
    quota_period_id     UUID        NOT NULL REFERENCES tenant_quota_periods(id),
    usage_count         INT         NOT NULL DEFAULT 1,
    source_type         VARCHAR(30),
                        -- 'queue_notification', 'marketing_campaign', 'otp', etc.
    source_id           UUID,
    idempotency_key     VARCHAR(255) UNIQUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_quota_ledger_period
    ON quota_usage_ledger(tenant_id, quota_period_id, created_at);

-- ===========================================================================
-- TIER 12: WEBHOOK INGRESS & OUTBOX
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- webhook_events
-- Transactional inbox for all Bhejna webhooks.
-- INSERT before returning 200 to Bhejna. Process asynchronously.
-- ON CONFLICT (source, external_event_id) DO NOTHING = idempotency.
-- Workers use SKIP LOCKED for safe parallel processing.
-- ---------------------------------------------------------------------------
CREATE TABLE webhook_events (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source              VARCHAR(50) NOT NULL,   -- 'bhejna'
    external_event_id   VARCHAR(255) NOT NULL,  -- bhejna_event_id
    event_type          VARCHAR(50),
                        -- 'message.received', 'flow.completed', 'message.status'
    tenant_id           UUID        REFERENCES tenants(id),
                        -- Resolved from business_phone_number by worker
    location_id         UUID        REFERENCES locations(id),
    payload             JSONB       NOT NULL,

    status              VARCHAR(20) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'processing', 'processed', 'failed')),
    attempts            INT         NOT NULL DEFAULT 0,
    last_error          TEXT,
    locked_until        TIMESTAMPTZ,
                        -- Used by SKIP LOCKED worker pattern

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at        TIMESTAMPTZ,

    UNIQUE(source, external_event_id)
);

CREATE INDEX idx_webhook_events_pending
    ON webhook_events(created_at)
    WHERE status IN ('pending', 'failed', 'processing');
    -- 'processing' included so the worker can reclaim dead leases (locked_until < NOW()).

-- ---------------------------------------------------------------------------
-- outbox_events
-- Transactional outbox pattern.
-- Written inside business transactions. Dispatched asynchronously.
-- Delivery is at-least-once. Exact-once semantics are enforced at the provider
-- layer: every Bhejna send MUST set idempotency_key = outbox_events.id so that
-- retries after a crash or timeout are deduped by Bhejna, not double-sent.
-- Workers use SKIP LOCKED for safe parallel processing.
-- ---------------------------------------------------------------------------
CREATE TABLE outbox_events (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        REFERENCES tenants(id),
    type            VARCHAR(100) NOT NULL,
                    -- 'notification.send'         — WhatsApp via Bhejna
                    -- 'feedback_request.schedule' — CompleteVisitAndCheckout step 13
                    -- 'appointment.reminder'      — appointment created
                    -- 'weekly_summary.send'       — Sunday 10PM cron
                    -- 'web_push.send'             — CompleteVisitAndCheckout step 12.5
                    --                               bypasses Bhejna quota (16_web_push_service_worker.md)
    payload         JSONB       NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'processing', 'dispatched', 'failed')),
    attempts        INT         NOT NULL DEFAULT 0,
    max_attempts    INT         NOT NULL DEFAULT 3,
    last_error      TEXT,
    process_after   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    -- Allows delayed processing (feedback: now + 30min)
    locked_until    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    dispatched_at   TIMESTAMPTZ
);

CREATE INDEX idx_outbox_pending
    ON outbox_events(process_after)
    WHERE status IN ('pending', 'failed', 'processing');
    -- 'processing' included so the claim-or-reclaim poll reclaims rows whose worker
    -- died mid-dispatch (locked_until < NOW()) using this index.

-- ===========================================================================
-- TIER 13: IDEMPOTENCY KEYS
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- idempotency_keys
-- Deduplication table for client-submitted idempotency keys.
-- Checked at API entry before any business logic executes.
-- Stores response so identical retries get identical responses.
-- ---------------------------------------------------------------------------
CREATE TABLE idempotency_keys (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    key             VARCHAR(100) NOT NULL,
                    -- Client-generated UUID v4
    endpoint        VARCHAR(100) NOT NULL,
                    -- e.g. 'queue.join', 'appointment.book'
    response_status INT,
    response_body   JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours',

    UNIQUE(tenant_id, key, endpoint)
);

CREATE INDEX idx_idempotency_keys_lookup
    ON idempotency_keys(tenant_id, key, endpoint);
    -- expires_at > NOW() filter lives in the query WHERE, not the index predicate:
    -- NOW() is not IMMUTABLE and a NOW()-based predicate aborts the migration.

-- ===========================================================================
-- TIER 14: MARKETING (Phase 2 tables, schema-ready now)
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- customer_consents
-- Explicit consent records per customer per channel per type.
-- marketing_opt_in BOOLEAN on customers is the Phase 1 shortcut.
-- This table is the Phase 2 granular version. Both coexist.
-- ---------------------------------------------------------------------------
CREATE TABLE customer_consents (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    customer_id     UUID        NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    channel         VARCHAR(20) NOT NULL CHECK (channel IN ('whatsapp', 'sms', 'email')),
    consent_type    VARCHAR(20) NOT NULL
                    CHECK (consent_type IN ('transactional', 'marketing')),
    status          VARCHAR(20) NOT NULL
                    CHECK (status IN ('opted_in', 'opted_out')),
    source          VARCHAR(50),
                    -- 'queue_join', 'web_form', 'staff_input', 'whatsapp_stop'
    captured_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, customer_id, channel, consent_type)
);

-- ---------------------------------------------------------------------------
-- marketing_campaigns
-- Schema-ready for Phase 2. Not exposed in Phase 1 UI.
-- Owners can see campaign history from Google Sheet until then.
-- ---------------------------------------------------------------------------
CREATE TABLE marketing_campaigns (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES tenants(id),
    location_id     UUID        REFERENCES locations(id),
    name            VARCHAR(255) NOT NULL,
    channel         VARCHAR(20) NOT NULL CHECK (channel IN ('whatsapp', 'sms')),
    campaign_type   VARCHAR(30) NOT NULL
                    CHECK (campaign_type IN (
                        'winback',
                        'slow_day',
                        'manual_broadcast',
                        'birthday',
                        'custom'
                    )),
    status          VARCHAR(20) NOT NULL DEFAULT 'draft'
                    CHECK (status IN (
                        'draft', 'scheduled', 'sending', 'sent', 'cancelled', 'failed'
                    )),
    audience_filter JSONB       NOT NULL DEFAULT '{}',
                    -- e.g. {"inactive_days": 30, "min_visits": 1}
    message_template TEXT       NOT NULL,
    scheduled_at    TIMESTAMPTZ,
    sent_at         TIMESTAMPTZ,
    recipient_count INT         NOT NULL DEFAULT 0,
    sent_count      INT         NOT NULL DEFAULT 0,
    created_by      UUID        REFERENCES staff_members(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ===========================================================================
-- TRIGGERS: updated_at maintenance
-- ===========================================================================

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_tenants_updated_at
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_locations_updated_at
    BEFORE UPDATE ON locations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_staff_members_updated_at
    BEFORE UPDATE ON staff_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_service_variants_updated_at
    BEFORE UPDATE ON service_variants
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_customers_updated_at
    BEFORE UPDATE ON customers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_appointments_updated_at
    BEFORE UPDATE ON appointments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_visit_charges_updated_at
    BEFORE UPDATE ON visit_charges
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_products_updated_at
    BEFORE UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_feedback_requests_updated_at
    BEFORE UPDATE ON feedback_requests
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_tenant_quota_periods_updated_at
    BEFORE UPDATE ON tenant_quota_periods
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_marketing_campaigns_updated_at
    BEFORE UPDATE ON marketing_campaigns
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ===========================================================================
-- SEED DATA: Default plans reference (documentation only, not enforced in DB)
-- ===========================================================================

-- Starter:  ₹299/mo  | 100 marketing msgs | 1000 transactional | 5 staff | 1 location
-- Growth:   ₹599/mo  | 300 marketing msgs | 3000 transactional | 10 staff | 2 locations
-- Pro:      ₹999/mo  | 500 marketing msgs | 5000 transactional | 20 staff | 5 locations
--
-- These are value-based quotes, not rigidly enforced tiers.
-- Actual limits stored directly on tenants table.
-- Add plans/tenant_subscriptions tables at 50+ shops when Google Sheet breaks.

COMMIT;
