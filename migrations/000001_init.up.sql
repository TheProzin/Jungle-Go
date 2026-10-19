CREATE TABLE wallets (
    id uuid PRIMARY KEY,
    player_id uuid NOT NULL,
    currency char(3) NOT NULL,
    balance_minor bigint NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT wallets_player_currency_unique UNIQUE (player_id, currency),
    CONSTRAINT wallets_balance_non_negative CHECK (balance_minor >= 0),
    CONSTRAINT wallets_version_positive CHECK (version >= 1),
    CONSTRAINT wallets_currency_format CHECK (currency ~ '^[A-Z]{3}$')
);

CREATE TABLE wager_transactions (
    id uuid PRIMARY KEY,
    origin text NOT NULL,
    kind text NOT NULL,
    status text NOT NULL,
    provider_id text,
    external_transaction_id text,
    idempotency_key text,
    payload_hash text,
    internal_reference text,
    wallet_id uuid NOT NULL REFERENCES wallets (id),
    player_id uuid NOT NULL,
    round_id text,
    game_id text,
    amount_minor bigint NOT NULL,
    currency char(3) NOT NULL,
    reference_external_transaction_id text,
    reference_transaction_id uuid REFERENCES wager_transactions (id),
    failure_code text,
    result_balance_minor bigint,
    result_currency char(3),
    reference_attempts integer NOT NULL DEFAULT 0,
    next_reference_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    processed_at timestamptz,
    CONSTRAINT wager_origin_valid CHECK (origin IN ('INTERNAL', 'EXTERNAL')),
    CONSTRAINT wager_kind_valid CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
    CONSTRAINT wager_status_valid CHECK (status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')),
    CONSTRAINT wager_amount_non_negative CHECK (amount_minor >= 0),
    CONSTRAINT wager_loss_amount_zero CHECK (kind <> 'LOSS' OR amount_minor = 0),
    CONSTRAINT wager_positive_amount_kinds CHECK (kind NOT IN ('BET', 'WIN', 'REFUND', 'ROLLBACK') OR amount_minor > 0),
    CONSTRAINT wager_opening_positive CHECK (kind <> 'OPENING' OR amount_minor > 0),
    CONSTRAINT wager_internal_is_opening CHECK (origin <> 'INTERNAL' OR kind = 'OPENING'),
    CONSTRAINT wager_opening_is_internal CHECK (kind <> 'OPENING' OR origin = 'INTERNAL'),
    CONSTRAINT wager_opening_internal_reference CHECK (kind <> 'OPENING' OR internal_reference IS NOT NULL),
    CONSTRAINT wager_opening_fields CHECK (
        kind <> 'OPENING' OR (
            provider_id IS NULL
            AND external_transaction_id IS NULL
            AND idempotency_key IS NULL
            AND payload_hash IS NULL
            AND round_id IS NULL
            AND game_id IS NULL
            AND reference_external_transaction_id IS NULL
            AND reference_transaction_id IS NULL
        )
    ),
    CONSTRAINT wager_external_fields CHECK (
        origin <> 'EXTERNAL' OR (
            provider_id IS NOT NULL
            AND external_transaction_id IS NOT NULL
            AND idempotency_key IS NOT NULL
            AND payload_hash IS NOT NULL
            AND round_id IS NOT NULL
            AND game_id IS NOT NULL
        )
    ),
    CONSTRAINT wager_reference_required CHECK (
        kind NOT IN ('REFUND', 'ROLLBACK') OR reference_external_transaction_id IS NOT NULL
    ),
    CONSTRAINT wager_failure_code_required CHECK (
        status NOT IN ('REJECTED', 'FAILED') OR failure_code IS NOT NULL
    ),
    CONSTRAINT wager_terminal_processed_at CHECK (
        status NOT IN ('PROCESSED', 'REJECTED', 'FAILED') OR processed_at IS NOT NULL
    ),
    CONSTRAINT wager_processed_result CHECK (
        status <> 'PROCESSED' OR result_balance_minor IS NOT NULL
    ),
    CONSTRAINT wager_result_balance_non_negative CHECK (
        result_balance_minor IS NULL OR result_balance_minor >= 0
    ),
    CONSTRAINT wager_result_pair CHECK (
        (result_balance_minor IS NULL) = (result_currency IS NULL)
    ),
    CONSTRAINT wager_pending_reference_schedule CHECK (
        status <> 'PENDING_REFERENCE' OR next_reference_at IS NOT NULL
    ),
    CONSTRAINT wager_reference_attempts_non_negative CHECK (reference_attempts >= 0),
    CONSTRAINT wager_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT wager_result_currency_format CHECK (
        result_currency IS NULL OR result_currency ~ '^[A-Z]{3}$'
    )
);

CREATE UNIQUE INDEX uq_tx_provider_external
    ON wager_transactions (provider_id, external_transaction_id)
    WHERE origin = 'EXTERNAL';

CREATE UNIQUE INDEX uq_tx_provider_idempotency
    ON wager_transactions (provider_id, idempotency_key)
    WHERE origin = 'EXTERNAL';

CREATE UNIQUE INDEX uq_tx_opening_wallet
    ON wager_transactions (wallet_id)
    WHERE kind = 'OPENING';

CREATE UNIQUE INDEX uq_tx_opening_internal_reference
    ON wager_transactions (internal_reference)
    WHERE kind = 'OPENING';

CREATE UNIQUE INDEX uq_tx_single_reversal
    ON wager_transactions (reference_transaction_id)
    WHERE kind IN ('REFUND', 'ROLLBACK')
      AND status = 'PROCESSED';

CREATE INDEX ix_tx_pending_reference
    ON wager_transactions (next_reference_at)
    WHERE status = 'PENDING_REFERENCE';

CREATE INDEX ix_tx_provider_status
    ON wager_transactions (provider_id, status);

CREATE INDEX ix_tx_wallet_created
    ON wager_transactions (wallet_id, created_at, id);

CREATE TABLE wallet_ledger_entries (
    id uuid PRIMARY KEY,
    wallet_id uuid NOT NULL REFERENCES wallets (id),
    transaction_id uuid NOT NULL REFERENCES wager_transactions (id),
    direction text NOT NULL,
    amount_minor bigint NOT NULL,
    currency char(3) NOT NULL,
    balance_before_minor bigint NOT NULL,
    balance_after_minor bigint NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT ledger_wallet_transaction_unique UNIQUE (wallet_id, transaction_id),
    CONSTRAINT ledger_direction_valid CHECK (direction IN ('DEBIT', 'CREDIT')),
    CONSTRAINT ledger_amount_positive CHECK (amount_minor > 0),
    CONSTRAINT ledger_balance_non_negative CHECK (
        balance_before_minor >= 0 AND balance_after_minor >= 0
    ),
    CONSTRAINT ledger_balance_math CHECK (
        (direction = 'CREDIT' AND balance_after_minor = balance_before_minor + amount_minor)
        OR
        (direction = 'DEBIT' AND balance_after_minor = balance_before_minor - amount_minor)
    ),
    CONSTRAINT ledger_currency_format CHECK (currency ~ '^[A-Z]{3}$')
);

CREATE INDEX ix_ledger_wallet_cursor
    ON wallet_ledger_entries (wallet_id, created_at, id);

CREATE TABLE inbox_messages (
    id bigserial PRIMARY KEY,
    consumer_name text NOT NULL,
    message_id text NOT NULL,
    payload_hash text NOT NULL,
    status text NOT NULL,
    received_at timestamptz NOT NULL,
    completed_at timestamptz,
    last_error text,
    CONSTRAINT inbox_consumer_message_unique UNIQUE (consumer_name, message_id),
    CONSTRAINT inbox_status_valid CHECK (status IN ('RECEIVED', 'COMPLETED', 'FAILED')),
    CONSTRAINT inbox_message_id_not_empty CHECK (length(message_id) > 0),
    CONSTRAINT inbox_payload_hash_not_empty CHECK (length(payload_hash) > 0),
    CONSTRAINT inbox_completed_at_required CHECK (
        status <> 'COMPLETED' OR completed_at IS NOT NULL
    )
);

CREATE INDEX ix_inbox_status_received
    ON inbox_messages (status, received_at);

CREATE TABLE outbox_events (
    event_id uuid PRIMARY KEY,
    aggregate_type text NOT NULL,
    aggregate_id text NOT NULL,
    event_type text NOT NULL,
    event_version integer NOT NULL,
    payload jsonb NOT NULL,
    correlation_id text NOT NULL,
    causation_id text,
    occurred_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    last_error text,
    published_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT outbox_event_version_positive CHECK (event_version >= 1),
    CONSTRAINT outbox_attempts_non_negative CHECK (attempts >= 0),
    CONSTRAINT outbox_event_type_not_empty CHECK (length(event_type) > 0),
    CONSTRAINT outbox_aggregate_id_not_empty CHECK (length(aggregate_id) > 0)
);

CREATE INDEX ix_outbox_pending
    ON outbox_events (next_attempt_at, occurred_at)
    WHERE published_at IS NULL;
