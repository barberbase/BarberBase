## 1. Core Realtime Manager

- [x] 1.1 Create `internal/realtime/manager.go` implementing `SSEEvent` and `Manager` with sync.Map and local mutex per location
- [x] 1.2 Implement Subscribe, Unsubscribe, non-blocking Broadcast, and StartHeartbeats loop

## 2. API Server Integration

- [x] 2.1 Edit `internal/api/server.go` to add `Manager *realtime.Manager` to `Server` struct
- [x] 2.2 Edit `cmd/server/main.go` to instantiate `Manager`, start heartbeats, and assign it to the `Server` struct

## 3. Handlers and Authentication

- [x] 3.1 Edit `internal/api/handlers_staff.go` to implement `SubscribeToQueueStream` with StaffJWT and CustomerSession HMAC query param verification
- [x] 3.2 Implement `GetQueueSnapshot` with batch fetches for service variants, customer notes, and per-location visit counts
- [x] 3.3 Replace reflection-based `Broadcast` calls in `CallNextCustomer` and `StartService` inside `handlers_staff.go` with direct calls

## 4. Integration Tests

- [x] 4.1 Write `internal/api/sse_test.go` verifying concurrent subscribers, disconnect cleanup, transaction rollback producing zero broadcasts, snapshot reflecting REST mutations after client reconnect, and no active session empty entries behavior
