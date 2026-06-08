## 1. Customer Identity & Repository

- [x] 1.1 Implement normalizeE164 in customer repository
- [x] 1.2 Implement ResolveOrCreateCustomer in internal/repository/customer.go
- [x] 1.3 Implement ResolveCustomerIdentity in internal/domain/identity/resolver.go
- [x] 1.4 Implement MergeShadowProfile in internal/domain/identity/merge.go

## 2. Message Classification

- [x] 2.1 Implement webhook package-level types and ClassifiedMessage struct
- [x] 2.2 Implement Classify in internal/webhook/message_classifier.go

## 3. Intent Resolution

- [x] 3.1 Implement IntentResolver struct and ResolveJoin method in internal/webhook/intent_resolver.go
- [x] 3.2 Implement magic link token generation and hashing in ResolveJoin
- [x] 3.3 Implement outbox_events payload construction and insertion in ResolveJoin
- [x] 3.4 Implement SSE Broadcast integration in ResolveJoin

## 4. Processor Loop

- [x] 4.1 Implement Processor struct and worker loop in internal/webhook/processor.go
- [x] 4.2 Implement event dispatch table and action handlers (ActionOnTheWay, ActionCancel, ActionRatingButton, ActionPlainRating, ActionOptOutButton/ActionStop) with tenant resolution from entity UUID chains in processor.go
- [x] 4.3 Implement panic recovery and retry status updates in processor.go

## 5. Verification & Tests

- [x] 5.1 Implement unit tests for normalizeE164 and Classify
- [x] 5.2 Implement integration tests for concurrent worker claiming, duplicate delivery, lease recovery, and attempts=10 exclusion
- [x] 5.3 Implement integration tests for JOIN resolution (including exact token_code match with % and _ in slug), duplicate checks, and shadow merges
- [x] 5.4 Run make build and make test to verify all pass

