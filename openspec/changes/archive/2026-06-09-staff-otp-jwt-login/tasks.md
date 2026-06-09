## 1. Server Struct Refactoring

- [x] 1.1 Create internal/api/server.go with api.Server struct and injected dependencies
- [x] 1.2 Modify cmd/server/main.go to delete ApiServer and instantiate api.Server

## 2. Core Auth and Middleware Implementation

- [x] 2.1 Create internal/auth/otp.go implementing per-phone rate limiting
- [x] 2.2 Create internal/auth/jwt.go with claims, signing, and verification logic
- [x] 2.3 Create internal/auth/middleware.go with JWT verification middleware and context helpers
- [x] 2.4 Create pkg/middleware/tenant.go with body tenant_id rejection middleware

## 3. Handler Implementation

- [x] 3.1 Implement RequestStaffOTP handler in internal/api/handlers_staff.go
- [x] 3.2 Implement VerifyStaffOTP handler in internal/api/handlers_staff.go
- [x] 3.3 Implement RefreshStaffToken handler in internal/api/handlers_staff.go
- [x] 3.4 Implement stubs/placeholders for remaining staff queue endpoints in internal/api/handlers_staff.go

## 4. Verification and Testing

- [x] 4.1 Build the application using make build
- [x] 4.2 Add integration tests verifying OTP replay, lockouts, expiration, concurrency, and tenant middleware
- [x] 4.3 Verify that all tests pass successfully using make test
