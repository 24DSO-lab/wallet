# Frontend decisions

## Bitcoin RBF
The project owner confirmed on 2026-11-16 that Bitcoin Replace-By-Fee (RBF) is included in version 1. This overrides the ADR-001 roadmap sentence deferring RBF until after version 1 stabilizes. ADR-001 is internally inconsistent: its Bitcoin adapter capability row already says RBF is enabled while the roadmap paragraph defers it. The owner-confirmed decision governs implementation. The ADR owner should reconcile both passages in a dedicated documentation change; this frontend note does not silently amend ADR-001.

Go owns replacement eligibility, fee policy, validation and transaction construction. The frontend only exposes replacement when Go reports it is eligible. Any RBF DTO/Wails methods must be reconciled with Dev 7 before production integration.

## Network profile
Bitcoin test environment is Bitcoin testnet. The active profile is application-wide and visible throughout the UI.