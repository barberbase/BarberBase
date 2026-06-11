## 1. Repository Layer

- [x] 1.1 Create `internal/repository/service.go` and define structs and sentinel error `ErrVariantExists`
- [x] 1.2 Implement `ListServicesForAdmin` fetching and assembling the hierarchy
- [x] 1.3 Implement `CreateServiceVariant` in an atomic transaction
- [x] 1.4 Implement `UpdateServiceVariant` performing a dynamic UPDATE and returning the updated variant

## 2. Handlers Layer

- [x] 2.1 Create `internal/api/handlers_admin.go`
- [x] 2.2 Implement `GetAdminLocationsLocationIdServices` handler
- [x] 2.3 Implement `CreateServiceVariant` handler mapping unique constraint errors to 409
- [x] 2.4 Implement `UpdateServiceVariant` handler enforcing allowed PATCH fields and returning 200

## 3. Verification & Testing

- [x] 3.1 Create integration tests verifying role gate, location tenant checks, duplicate variant conflicts, immutable booking rules, and visit_services snapshots preservation
- [x] 3.2 Run `make build` and `make test` to verify everything builds and passes
