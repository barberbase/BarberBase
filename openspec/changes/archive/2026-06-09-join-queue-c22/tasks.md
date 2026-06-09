## 1. DB Repositories

- [ ] 1.1 Implement InsertVisit and InsertVisitServices in internal/repository/visit.go
- [ ] 1.2 Implement InsertQueueEntry and GetQueueEntryByCustomer in internal/repository/queue.go

## 2. Domain Commands

- [ ] 2.1 Implement JoinQueue in internal/domain/queue/commands.go with the 11-step transaction

## 3. API Handler

- [ ] 3.1 Implement JoinQueue handler in internal/api/handlers_public.go and wire to server

## 4. Tests

- [ ] 4.1 Implement integration tests for JoinQueue in internal/api/handlers_public_test.go
- [ ] 4.2 Verify all tests pass with make test
