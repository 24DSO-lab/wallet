# Bitcoin RBF testnet acceptance

This checklist is the release gate for the end-to-end Bitcoin Replace-By-Fee flow.
Automated unit and CI tests do not replace this network acceptance run.

## Safety boundary

- Use the application's `test` profile only. The expected Bitcoin address prefix is `tb1q`.
- Use a dedicated test-only wallet and testnet coins from a faucet. Never import a wallet that holds mainnet funds for this run.
- Confirm the profile and address prefix before every send. Stop immediately if the UI or explorer shows a mainnet address or mainnet transaction.
- Never share a seed phrase, private key, keystore password, or passphrase in logs, screenshots, issues, or chat.
- Do not run this procedure on mainnet. Testnet coins have no monetary value.

## Preconditions

- [ ] CI is green for the exact PR head commit on Ubuntu, Windows, frontend, and lint jobs.
- [ ] Start the desktop application with the `test` profile.
- [ ] Confirm the configured testnet Esplora primary and fallback endpoints are reachable.
- [ ] Create a dedicated test-only wallet and fund its native P2WPKH receive address with testnet BTC.
- [ ] Refresh until the testnet UTXOs are visible and sufficiently confirmed for the configured coin-selection policy.
- [ ] Record the application version, commit SHA, profile, and public transaction IDs only.

## Acceptance scenarios

### 1. Successful replacement

- [ ] Send a small testnet amount to a second address under your control. Ensure the confirmation quote identifies Bitcoin testnet.
- [ ] Broadcast the transaction and record its txid. Verify the transaction is unconfirmed and signals opt-in RBF (input sequence below `0xfffffffe`; this wallet uses `0xfffffffd`).
- [ ] In transaction history, confirm the original transaction is pending and the RBF action is available.
- [ ] Prepare an RBF replacement. Confirm the recipient, amount, and original inputs are unchanged; only the change output is reduced to fund the higher fee.
- [ ] Confirm the replacement fee is higher and satisfies the configured incremental relay fee policy.
- [ ] Approve the replacement in the application and record the replacement txid.
- [ ] Verify through the testnet explorer that the replacement is accepted and the original is replaced / no longer independently pending.
- [ ] Refresh the wallet. The original history row must be `replaced`; the replacement must be pending, then confirmed after mining.
- [ ] Confirm the final balance and fee reflect the replacement exactly once.

### 2. Duplicate and stale replacement protection

- [ ] While a replacement attempt is awaiting reconciliation, verify that the original transaction no longer offers another RBF action.
- [ ] Verify a second prepare/confirm attempt for the same original is rejected and does not create another signed transaction.
- [ ] If the original confirms before replacement confirmation, verify the replacement is rejected and no signature is produced.

### 3. Insufficient change and relay policy

- [ ] Use a test transaction whose change cannot cover the required fee increase while remaining above the dust threshold. RBF preparation must fail without signing or broadcasting.
- [ ] Verify an incremental relay fee rate that is invalid (zero, negative, or malformed configuration) prevents application startup rather than silently weakening the policy.
- [ ] If the target node's incremental relay fee differs from the default, set `BTC_INCREMENTAL_RELAY_FEE_RATE` to the node's actual policy and repeat the successful replacement.

### 4. Descendants and provider failures

- [ ] If any output of the original transaction has already been spent, RBF must fail closed; do not attempt package-fee estimation.
- [ ] If the outspends endpoint is unavailable or returns malformed data, RBF must fail closed.
- [ ] Simulate a broadcast timeout after submission. The replacement recovery record must remain durable, the original must not be prematurely deleted, and a duplicate attempt must be blocked.
- [ ] Restore provider connectivity and run reconciliation. Once the provider reports the replacement in the mempool, the original must transition to `replaced` and its pending payload must be retired.
- [ ] Verify a provider response that cannot establish acceptance does not cause the application to mark the original replaced.

## Evidence to attach to the release record

Record only non-secret evidence:

- [ ] PR head commit SHA and CI run URL.
- [ ] Testnet original and replacement txids.
- [ ] Explorer links for both txids.
- [ ] Before/after fee in satoshis, estimated vsize, and fee rate in sat/vB.
- [ ] Confirmation that recipient, amount, and input outpoints were preserved.
- [ ] History statuses and balance after reconciliation.
- [ ] Results for each failure scenario above, including provider and outspends failures.

Do not attach raw signed transaction payloads unless the reviewer explicitly needs them; never attach secret material.

## Release decision

RBF is accepted only when all applicable scenarios pass on testnet and the ADR-001 security/release review is signed off. Green CI alone is not sufficient. Mainnet broadcasting remains out of scope until that release decision is recorded.
