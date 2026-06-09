## 1. Domain Logic Implementation

- [ ] 1.1 Create `internal/domain/presence/arrival.go` and implement the IP rate limiter using `sync.Mutex` and `rate.Limiter`.
- [ ] 1.2 Implement the `ConfirmArrival` method carrying out per-IP rate checks, fetching location & attempt count, performing pre-transaction PIN/GPS/NFC validation, running the transaction (which locks `queue_sessions` and `queue_entries`), logging attempts, and returning success or structured errors.
- [ ] 1.3 Implement the `haversineMetres` helper function.
- [ ] 1.4 Implement the `ConfirmOnTheWay` method with transaction serialization (Law 1) and status updates.
- [ ] 1.5 Implement the `CancelMyEntry` method with transactional queue cancellation.
- [ ] 1.6 Implement the `StaffConfirmArrival` method with StaffJWT authentication context check, tenant location isolation, and transactional override check-in.

## 2. API Handlers and Routing Integration

- [ ] 2.1 Edit `internal/api/handlers_public.go` to extract CustomerSession context parameters (tenant, location, visit ID), parse request body, call the domain `Service` methods, map presence errors to status codes (400, 422, 429), and return the expected response payloads.
- [ ] 2.2 Edit `internal/api/handlers_staff.go` to define the staff arrival confirmation handler, extract staff context, and map error status codes (403, 404, 422).
- [ ] 2.3 Edit `cmd/server/main.go` to construct the `presence.Service` and inject it as the `Arrival` field on `api.Server`.

## 3. Verification and Testing

- [ ] 3.1 Run tests via `make build && make test` to ensure compilation and existing tests pass.
- [ ] 3.2 Implement or run unit/integration tests to verify wrong PIN rate limiting, GPS accuracy threshold, GPS distance boundaries, staff authentication/isolation, and cancellation states.
