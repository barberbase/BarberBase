# customer-identity Specification

## Purpose
TBD - created by archiving change webhook-worker-c13. Update Purpose after archive.
## Requirements
### Requirement: Customer identity lookup and creation
The system SHALL resolve the canonical customer ID for incoming webhook messages.
If the phone number is provided, it MUST look up the customer and create one if not found.
If the phone number is masked, it MUST look up by provider ID (BSUID) and create a shadow profile if not found.

#### Scenario: Lookup customer with phone
- **WHEN** phone is "+919876543210" and customer exists
- **THEN** it returns the existing customer ID.

#### Scenario: Create shadow profile for masked phone
- **WHEN** phone is empty and BSUID is provided
- **THEN** it inserts a new shadow profile customer, links it in `customer_identities`, and returns the shadow customer ID.

### Requirement: Shadow profile merge
The system SHALL support promoting a shadow profile to a real customer profile when a confirmed phone number arrives in the same session.
If another customer already owns that phone number, it MUST log a conflict warning and keep the profiles separate.

#### Scenario: Promote shadow profile
- **WHEN** a confirmed phone number is received for a shadow profile, and no other customer has this phone number
- **THEN** it updates the customer record's phone number, marks `is_shadow_profile = false`, and links the identity.

