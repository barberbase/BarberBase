## 1. Repository Implementation

- [x] 1.1 Define ErrTenantSlugConflict, ErrLocationSlugConflict, and ErrOwnerPhoneConflict sentinels in internal/repository/location.go
- [x] 1.2 Implement ProvisionTenant database transaction in internal/repository/location.go with unique constraint checking

## 2. Configuration & Boot Guard

- [x] 2.1 Add PlatformAdminKey field to Config struct in internal/config/config.go and load it from environment
- [x] 2.2 Add boot guard check to cmd/server/main.go to exit if PLATFORM_ADMIN_KEY is empty

## 3. Middleware, Handler & Routing

- [x] 3.1 Implement platformAdminKeyMiddleware in internal/api/handlers_admin.go
- [x] 3.2 Implement ProvisionTenant handler on *Server in internal/api/handlers_admin.go
- [x] 3.3 Register route POST /v1/admin/setup with platformAdminKeyMiddleware in cmd/server/main.go

## 4. Verification & Testing

- [x] 4.1 Write integration tests in internal/api/handlers_admin_test.go covering C5.3 requirements, including immediate OTP request and PIN bcrypt consistency
- [x] 4.2 Run and pass the test suite
