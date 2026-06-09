# queue-session-auto-create Specification

## Purpose
TBD - created by archiving change queue-session-auto-create. Update Purpose after archive.
## Requirements
### Requirement: Idempotent Queue Session Auto-creation
The system SHALL automatically create a queue session for the location and business date if it does not exist, and obtain a row-level lock on it inside the active transaction. To prevent concurrency races, the database operations MUST be executed in the exact order: first an idempotent `INSERT ON CONFLICT DO NOTHING`, then a blocking `SELECT FOR UPDATE`.

#### Scenario: Concurrent First-Joiners
- **WHEN** 50 concurrent transactions simultaneously attempt to ensure and lock a session on a fresh location and business date
- **THEN** all transactions succeed, exactly one queue session record is created, and the session's token number is initialized to 0

