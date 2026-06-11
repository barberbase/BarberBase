# services-crud Specification

## Purpose
TBD - created by archiving change implement-services-crud. Update Purpose after archive.
## Requirements
### Requirement: GET Service Catalog for Admin
The system SHALL list all active services under a location in a hierarchical format.
It MUST fetch `service_display_mode` from the locations table and check if the location belongs to the tenant.
It MUST verify that the staff member has the role of 'owner' or 'manager'.

#### Scenario: Successful GET Service Catalog
- **WHEN** an owner or manager requests the active service catalog for a valid location belonging to their tenant
- **THEN** the system returns HTTP 200 with the full service catalog nested hierarchy and the display mode of the location

#### Scenario: Unauthorized Access to GET Service Catalog
- **WHEN** a staff member with 'barber' role requests the service catalog
- **THEN** the system returns HTTP 403 Forbidden

#### Scenario: Non-existent or Unowned Location GET
- **WHEN** the location does not exist or belongs to a different tenant
- **THEN** the system returns HTTP 404 Not Found

### Requirement: POST Create Service Variant
The system SHALL create a new service variant under a location. If the category or group does not exist (or is soft-deleted/inactive), the system SHALL upsert (create or reactivate) them inside the same transaction.
It MUST verify that the staff member has the role of 'owner' or 'manager'.
It MUST verify that the variant name is unique within its group, returning HTTP 409 on conflict.

#### Scenario: Successful Variant Creation with Category and Group Upsert
- **WHEN** an owner or manager requests variant creation with a new or inactive category and group
- **THEN** the system upserts the category and group, inserts the variant in a single transaction, and returns HTTP 201 with the created variant details

#### Scenario: Duplicate Variant Name in Same Group
- **WHEN** a variant is created with a name that already exists within the same group
- **THEN** the transaction rolls back, and the system returns HTTP 409 Conflict

### Requirement: PATCH Update Service Variant
The system SHALL update a service variant's name, duration, price, active status, or popular status.
It MUST NOT allow updating booking rules (`allow_walk_in`, `allow_appointment`, `requires_appointment`).
It MUST NOT propagate variant updates to historical `visit_services` rows.

#### Scenario: Successful Variant Update
- **WHEN** an owner or manager updates a variant's price
- **THEN** the variant is updated, returning HTTP 200, and existing historical visit_services rows remain unchanged

