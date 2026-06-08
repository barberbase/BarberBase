## ADDED Requirements

### Requirement: E164 phone number normalization
The system SHALL normalize phone numbers to E.164 standard by stripping all non-digit characters and prepending a plus sign.
It MUST return an empty string if the input is empty or resolves to no digits. It MUST NOT automatically inject country codes.

#### Scenario: Normalizing standard phone number
- **WHEN** the input is "919876543210" or "  919876543210  "
- **THEN** it is normalized to "+919876543210".

#### Scenario: Normalizing empty phone number
- **WHEN** the input is "" or whitespace or nil
- **THEN** it is normalized to "".

### Requirement: Bhejna message classification
The system SHALL parse inbound webhooks from Bhejna and classify them into MessageActions.
If the event type is anything other than `message.received` (such as `message.status_updated`), it SHALL classify it accordingly.
For `message.received`, it SHALL extract sender phone number (normalized), BSUID, masked flag, display name, and dispatch by either text content (case-insensitive commands like JOIN, STOP/UNSUBSCRIBE, rating numbers) or interactive button payloads.

#### Scenario: Classify text JOIN command
- **WHEN** body text is "JOIN STAR-SALON JN8K4P"
- **THEN** action is classified as `ActionJoin`, token code as "JN8K4P", and slug as "STAR-SALON".

#### Scenario: Classify button payload ON_THE_WAY
- **WHEN** button payload is "ON_THE_WAY:019001b3-4f9c-70e1-8000-017f8a9b2c3d"
- **THEN** action is classified as `ActionOnTheWay` and entry ID is extracted.

#### Scenario: Classify plain rating text
- **WHEN** body text is " 4 "
- **THEN** action is classified as `ActionPlainRating` with rating 4.
