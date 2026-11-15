# Frontend implementation decisions

This file records frontend-facing decisions and constraints for Dev 8. The project-wide source of truth remains `docs/ADR-001-desktop-wallet.md`; this note does not silently amend that ADR.

## Approved project decision: Bitcoin RBF in version 1

**Decision (confirmed by the project owner on 2026-11-16): enable Replace-By-Fee (RBF) immediately, in version 1.**

This intentionally overrides the deferred-RBF sentence in ADR-001, which currently says that accelerating stuck Bitcoin transactions (RBF) and replacing Ethereum transactions by nonce should be added after version 1 stabilizes.

There is also an internal inconsistency in ADR-001: its Bitcoin adapter capability row already says that RBF is enabled, while the later roadmap paragraph defers RBF. The owner-confirmed decision resolves this ambiguity for implementation: **Bitcoin RBF is in scope from the first release.**

The ADR owner should reconcile the two passages in a dedicated documentation change. Dev 8 does not edit the project-wide ADR from the frontend ownership area.

## Frontend implications

- The UI must present replacement/fee-bump actions only when the Go API reports that a transaction is eligible for replacement.
- The frontend must not decide eligibility, calculate replacement fees, construct replacement transactions, or infer mempool policy. Those decisions belong to Go.
- Any RBF-specific DTO fields or Wails methods must be agreed with Dev 7/Dev 4 through the existing contract process before being consumed here.
- Until the backend contract is available, the frontend must not invent DTO fields or simulate successful replacement behavior in the production Wails client.
- Mock behavior may model the approved user flow only after its shape is aligned with the backend contract.

## Other confirmed network choice

Bitcoin's test environment for the project is **Bitcoin testnet**. The UI must make the active network/profile visible and must never infer a network from an address or user-entered text.
