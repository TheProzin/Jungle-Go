CREATE OR REPLACE FUNCTION reject_ledger_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'wallet_ledger_entries is append-only';
END;
$$;

CREATE TRIGGER trg_ledger_no_update
BEFORE UPDATE ON wallet_ledger_entries
FOR EACH ROW
EXECUTE FUNCTION reject_ledger_mutation();

CREATE TRIGGER trg_ledger_no_delete
BEFORE DELETE ON wallet_ledger_entries
FOR EACH ROW
EXECUTE FUNCTION reject_ledger_mutation();

CREATE OR REPLACE FUNCTION validate_ledger_entry()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    transaction_wallet uuid;
    transaction_currency char(3);
    wallet_currency char(3);
BEGIN
    SELECT wallet_id, currency
      INTO transaction_wallet, transaction_currency
      FROM wager_transactions
     WHERE id = NEW.transaction_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'ledger transaction % does not exist', NEW.transaction_id;
    END IF;

    SELECT currency
      INTO wallet_currency
      FROM wallets
     WHERE id = NEW.wallet_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'ledger wallet % does not exist', NEW.wallet_id;
    END IF;

    IF transaction_wallet <> NEW.wallet_id THEN
        RAISE EXCEPTION 'ledger wallet does not match transaction wallet';
    END IF;

    IF transaction_currency <> NEW.currency OR wallet_currency <> NEW.currency THEN
        RAISE EXCEPTION 'ledger currency does not match wallet and transaction currency';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_ledger_validate
BEFORE INSERT ON wallet_ledger_entries
FOR EACH ROW
EXECUTE FUNCTION validate_ledger_entry();

CREATE OR REPLACE FUNCTION reject_terminal_transaction_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status IN ('PROCESSED', 'REJECTED', 'FAILED') THEN
        RAISE EXCEPTION 'terminal transaction % cannot transition from %', OLD.id, OLD.status;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_tx_terminal_immutable
BEFORE UPDATE ON wager_transactions
FOR EACH ROW
EXECUTE FUNCTION reject_terminal_transaction_update();

CREATE OR REPLACE FUNCTION reject_transaction_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'wager_transactions cannot be deleted';
END;
$$;

CREATE TRIGGER trg_tx_no_delete
BEFORE DELETE ON wager_transactions
FOR EACH ROW
EXECUTE FUNCTION reject_transaction_delete();

CREATE OR REPLACE FUNCTION reject_outbox_identity_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.event_id IS DISTINCT FROM OLD.event_id
       OR NEW.event_type IS DISTINCT FROM OLD.event_type
       OR NEW.event_version IS DISTINCT FROM OLD.event_version
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.occurred_at IS DISTINCT FROM OLD.occurred_at THEN
        RAISE EXCEPTION 'outbox event identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_outbox_identity_immutable
BEFORE UPDATE ON outbox_events
FOR EACH ROW
EXECUTE FUNCTION reject_outbox_identity_update();

CREATE OR REPLACE FUNCTION reject_wallet_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'wallets cannot be deleted';
END;
$$;

CREATE TRIGGER trg_wallet_no_delete
BEFORE DELETE ON wallets
FOR EACH ROW
EXECUTE FUNCTION reject_wallet_delete();
