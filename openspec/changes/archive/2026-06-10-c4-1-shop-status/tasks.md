## 1. Repository Layer

- [x] 1.1 Create `internal/repository/location.go` and define types: `ShopStatusResult`, `StaffShopStatus`, `SetShopStatusParams`, and error `ErrActiveEntriesExist`.
- [x] 1.2 Implement `GetEffectiveShopStatus` to fetch the current active override.
- [x] 1.3 Implement `GetStaffShopStatus` to return the composite status from overrides, queue session, and location tables.
- [x] 1.4 Implement `SetShopStatus` transaction flow: check active entries, lock queue session, determine new session status, write override, update session, bulk expire entries if needed, and commit.

## 2. API Handlers Layer

- [x] 2.1 Verify operationId/generated type names in `generated.go` for `/staff/shop/status` endpoints.
- [x] 2.2 Implement GET handler for `/staff/shop/status` in `internal/api/handlers_staff.go` adhering to Law 11.
- [x] 2.3 Implement PATCH handler for `/staff/shop/status` in `internal/api/handlers_staff.go` handling request parsing, logic routing, and translation of `ErrActiveEntriesExist` to 422.

## 3. Verification

- [x] 3.1 Verify all project constraints are met.
- [x] 3.2 Add integration test for expired overrides: verify `GetEffectiveShopStatus` returns no active override when the latest row's `expires_at` is in the past.
- [x] 3.3 Run `make build` and `make test`.
