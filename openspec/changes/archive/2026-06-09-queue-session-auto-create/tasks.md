## 1. Core Implementation

- [x] 1.1 Implement EnsureAndLockQueueSession in internal/repository/queue.go
- [x] 1.2 Implement Commands service and lockSession in internal/domain/queue/commands.go

## 2. Testing & Verification

- [x] 2.1 Implement concurrent integration test TestEnsureAndLockQueueSession_ConcurrentFirstJoiners in internal/repository/queue_test.go
- [x] 2.2 Verify implementation compiles and tests pass using go test
