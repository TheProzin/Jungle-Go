DROP TRIGGER IF EXISTS trg_wallet_no_delete ON wallets;
DROP FUNCTION IF EXISTS reject_wallet_delete();

DROP TRIGGER IF EXISTS trg_outbox_identity_immutable ON outbox_events;
DROP FUNCTION IF EXISTS reject_outbox_identity_update();

DROP TRIGGER IF EXISTS trg_tx_no_delete ON wager_transactions;
DROP FUNCTION IF EXISTS reject_transaction_delete();

DROP TRIGGER IF EXISTS trg_tx_terminal_immutable ON wager_transactions;
DROP FUNCTION IF EXISTS reject_terminal_transaction_update();

DROP TRIGGER IF EXISTS trg_ledger_validate ON wallet_ledger_entries;
DROP FUNCTION IF EXISTS validate_ledger_entry();

DROP TRIGGER IF EXISTS trg_ledger_no_delete ON wallet_ledger_entries;
DROP TRIGGER IF EXISTS trg_ledger_no_update ON wallet_ledger_entries;
DROP FUNCTION IF EXISTS reject_ledger_mutation();
