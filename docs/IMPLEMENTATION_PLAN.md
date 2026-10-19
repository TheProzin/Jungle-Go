# Plano de Implementação — Jungle-Go

Documento executável de implementação do desafio descrito em [`README.md`](../README.md).
Cada fase possui entregáveis e critérios de aceite verificáveis. O objetivo é atender
**todos** os requisitos obrigatórios com código simples, explícito e testável.

---

## 1. Análise do estado atual

- O repositório contém apenas o `README.md` do desafio.
- Não existe código, `go.mod`, migrations, Docker Compose ou documentação de solução.
- O ambiente local possui Go 1.27.2, Docker e Docker Compose.
- A implementação parte do zero.

### 1.1 Requisitos agrupados

| Grupo | Requisitos principais |
| --- | --- |
| Dinheiro | `int64` em unidades mínimas, sem float, parsing estrito, overflow, moedas |
| Carteira | Agregado, versão, saldo não negativo, ledger append-only |
| Transações | `OPENING`, `BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK`, máquina de estados |
| Idempotência | Persistente, hash canônico, replay, conflito de chave/conteúdo |
| Concorrência | Locks por carteira, sem lost update, 3 instâncias independentes |
| Referências | `PENDING_REFERENCE`, retry com backoff, TTL, rejeição estável |
| Mensageria | SQS FIFO, inbox, DLQ, ack após commit, shutdown seguro |
| Outbox | Publicação após commit, múltiplos publishers, retry, `eventId` estável |
| Auth | OAuth2/OIDC real, `client_credentials`, isolamento por `providerId` |
| Observabilidade | Logs JSON, métricas, health checks, reconciliação |
| Entrega | Compose, migrations, README, `ARCHITECTURE.md`, `.env.example`, testes |

### 1.2 Critérios eliminatórios

A solução **não pode**:

1. Deixar endpoints de negócio sem autenticação efetiva.
2. Permitir acesso a operações/transações de outro provedor.
3. Usar `float32`/`float64` em qualquer etapa financeira.
4. Permitir saldo negativo por concorrência.
5. Gerar movimentação duplicada.
6. Manter idempotência apenas em memória.
7. Depender de uma única instância.
8. Publicar eventos antes do commit.
9. Deixar de ter ledger auditável.
10. Substituir PostgreSQL, SQS e IdP por mocks nos testes de integração.

---

## 2. Decisões arquiteturais

### 2.1 Dinheiro

- Representação: `int64` em **centavos** + código ISO 4217 (`BRL`).
- `Money` é um value object imutável.
- Parsing apenas por string decimal com escala fixa de 2 casas.
- Filtro sintático: `^(0|[1-9][0-9]*)\.[0-9]{2}$`. Zeros à esquerda, sinal, espaço, vírgula e escala diferente de 2 casas são rejeitados.
- O filtro sintático não garante o limite: após converter para centavos, o parsing rejeita com `ErrOverflow` qualquer valor fora de `int64`.
- Limite representável: `±92_233_720_368_547_758.07` (centavos em `int64`).
- Rejeitar: vazio, sinal, `NaN`, `Infinity`, notação científica, vírgula, escala diferente de 2 casas e overflow.
- Aritmética com verificação de overflow/underflow.
- `MarshalJSON` e `UnmarshalJSON` sempre como string decimal com 2 casas.
- Moedas diferentes em soma/subtração/comparação retornam erro de domínio.
- Apenas `BRL` é aceito nas entradas externas nesta entrega; o tipo suporta qualquer ISO 4217 válido.

### 2.2 Persistência

- PostgreSQL 16.
- `pgx/v5` com `pgxpool` e SQL explícito.
- Migrations com `golang-migrate`, embutidas na aplicação via `go:embed`.
- Nenhum ORM.
- Transações delimitadas na camada de aplicação, nunca dentro do domínio.

### 2.3 Concorrência

- Lock pessimista por carteira: `SELECT ... FOR UPDATE`.
- Atualização condicionada à versão: `UPDATE ... WHERE id = $1 AND version = $2`.
- Constraints e índices únicos como última linha de defesa.
- Sem locks globais e sem advisory lock global.
- Retry limitado (3 tentativas com jitter) para `40001` (serialization) e `40P01` (deadlock).
- Ordem de lock: **sempre carteira primeiro**, depois referência.

### 2.4 Idempotência

- Chave de idempotência persistida em `(provider_id, idempotency_key)`.
- Identidade financeira persistida em `(provider_id, external_transaction_id)`.
- Hash SHA-256 de JSON canônico do payload de negócio.
- Chave e conteúdo iguais: replay com `idempotentReplay: true`.
- Chave igual e conteúdo diferente: `409 IDEMPOTENCY_KEY_CONFLICT`.
- Identidade externa igual com chave diferente: `409 EXTERNAL_TRANSACTION_CONFLICT`.
- O servidor **não** substitui a chave recebida por uma calculada.
- `Idempotency-Key` obrigatório, não vazio, com no máximo 255 caracteres; formato é opaco e não é validado além disso.

#### 2.4.1 Hash canônico

- Algoritmo: SHA-256, saída hexadecimal minúscula.
- Entrada: JSON canônico apenas com campos de negócio, em ordem alfabética fixa:
  `externalTransactionId`, `gameId`, `kind`, `money.amount`, `money.currency`, `playerId`,
  `providerId`, `referenceExternalTransactionId`, `roundId`, `walletId`.
- Excluídos: `Idempotency-Key`, headers HTTP, `messageId`, `occurredAt`, `type` do envelope e
  demais metadados de transporte.
- Normalizações:
  - `amount` sempre com 2 casas (`"25.00"`, `"0.00"`);
  - `currency` sempre maiúscula;
  - `referenceExternalTransactionId` ausente e `null` produzem o mesmo hash (campo omitido);
  - strings não sofrem trim silencioso; espaços tornam o payload inválido.
- HTTP e SQS constroem o mesmo struct canônico antes de calcular o hash.
- O hash documentado é o mesmo usado para detectar conflito de conteúdo.

### 2.5 Reversões

- `REFUND` só referencia `BET` processada.
- `ROLLBACK` referencia `BET`, `WIN` ou `REFUND` processada.
- A referência é resolvida por `(provider_id, referenceExternalTransactionId)`.
- O `Money` da reversão deve ser **exatamente igual** ao da transação referenciada;
  divergência retorna `422 REFERENCE_AMOUNT_MISMATCH`.
- A operação e a referência devem concordar em provedor, jogador, carteira, moeda e rodada.
- Direções do `ROLLBACK`:

| Original | `ROLLBACK` |
| --- | --- |
| `BET` (débito) | crédito |
| `WIN` (crédito) | débito |
| `REFUND` (crédito) | débito |

- `REFUND` e `ROLLBACK` são mutuamente exclusivos por transação original.
- Índice único parcial garante **no máximo uma reversão `PROCESSED` por `reference_transaction_id`**.
- A linha da transação referenciada é lockada com `FOR UPDATE` antes de aplicar a reversão.
- Se duas reversões concorrentes chegarem como `PENDING_REFERENCE`, a primeira que processar
  vence; a segunda, ao processar, detecta a reversão existente e retorna `DUPLICATE_REVERSAL`.
- `ROLLBACK` de `WIN`/`REFUND` que exigiria débito maior que o saldo retorna
  `422 INSUFFICIENT_FUNDS_FOR_REVERSAL`, distinto de `INSUFFICIENT_FUNDS`.
- Justificativa da exclusividade: permite no máximo uma devolução/desfazimento do mesmo
  débito original, evitando duplicidade financeira. Combinações adicionais exigiriam
  lançamentos corretivos novos.

#### 2.5.1 Referência opcional do `WIN`

- `WIN` pode informar `referenceExternalTransactionId` de um `BET` da **mesma rodada**.
- A referência é opcional e não altera o valor creditado.
- Se informada, o `BET` referenciado deve existir, ser do mesmo provedor, jogador, carteira,
  moeda e rodada e estar `PROCESSED`.
- Referência inválida ou de outra rodada retorna `422 REFERENCE_MISMATCH`.
- O campo participa do hash canônico quando presente.

### 2.6 Referências pendentes

- Operação sem referência: `PENDING_REFERENCE`.
- Worker separado consulta pendências com `FOR UPDATE SKIP LOCKED`.
- Backoff exponencial: `min(2^tentativa segundos, 5 minutos)`.
- Limite configurável: 10 tentativas **ou** TTL de 1 hora (o que ocorrer primeiro).
- Ao esgotar: `REJECTED` com `REFERENCE_NOT_FOUND` + evento `WagerTransactionRejected`.
- Referência existente mas não processada: continua pendente.
- Referência terminal rejeitada/falha: rejeita com `REFERENCE_NOT_PROCESSED`.
- Retomada durável entre reinícios e instâncias.

#### 2.6.1 Identidade interna do `OPENING`

- `OPENING` é criado exclusivamente por `POST /wallets`.
- Cada `OPENING` recebe `internalReference` estável (UUIDv7 ou chave idempotente gerada pelo serviço interno).
- `UNIQUE(internal_reference) WHERE kind='OPENING'` e `UNIQUE(wallet_id) WHERE kind='OPENING'`
  impedem crédito inicial duplicado, inclusive sob concorrência e reenvio.
- O `OPENING` não possui provider, ID externo, chave de idempotência externa, rodada, jogo ou referência.

#### 2.6.2 Falhas transitórias x permanentes

- Transitória (DB/SQS indisponível, timeout, deadlock após retries): rollback ou não-ack;
  a mensagem é reentregue e a operação **não** é persistida como `REJECTED`.
- Permanente de negócio (saldo, referência, moeda, duplicidade): persiste `REJECTED` com `failureCode`.
- Permanente de infraestrutura (payload irrecuperável, corrupção, tentativas esgotadas sem
  classificação de negócio): persiste `FAILED` com `failureCode` e envia a mensagem à DLQ.
- `FAILED` é terminal e auditável; nunca altera saldo.
- A distinção é feita por classificação tipada do erro (`TransientError`, `BusinessError`,
  `PermanentError`) e pelo limite de tentativas.

### 2.7 Inbox/outbox

- Inbox, transação, ledger, carteira e outbox na **mesma transação SQL**.
- Publisher da outbox usa `FOR UPDATE SKIP LOCKED` com lease.
- `eventId` gerado no momento da criação e nunca alterado em republicações.
- Consumidor de eventos deve deduplicar por `eventId`.
- Publicação sempre posterior ao commit por construção.

### 2.8 Uber Fx

- Binário único `cmd/app`.
- Módulos Fx: `config`, `observability`, `postgres`, `auth`, `repositories`,
  `usecases`, `http`, `sqs`, `outbox`, `workers`.
- Injeção por construtores.
- `fx.Lifecycle` para:
  - validar configuração na inicialização;
  - abrir/fechar pool;
  - iniciar/parar servidor HTTP;
  - iniciar/parar consumer SQS;
  - iniciar/parar publisher e worker de referências;
  - aguardar término dos workers antes de fechar dependências.

### 2.9 Autenticação e autorização

- Keycloak como IdP OIDC.
- Fluxo `client_credentials`.
- Clientes:
  - `internal-service`: role `internal`.
  - `provider-a`: role `provider`, claim `provider_id=provider-a`.
  - `provider-b`: role `provider`, claim `provider_id=provider-b`.
- Validação JWT com JWKS, `iss`, `aud`, `exp` e `nbf`.
- Sem cadastro de senhas e sem emissão própria de tokens.

### 2.10 Testes

- Unitários com `testing`.
- Integração com Docker Compose dedicado: PostgreSQL, LocalStack e Keycloak reais.
- Build tag `integration`.
- Testes de concorrência e recuperação com 3 instâncias.
- `go test -race` nos pacotes aplicáveis.

---

## 3. Estrutura de pastas

```text
.
├── cmd/
│   └── app/
│       └── main.go
├── internal/
│   ├── config/
│   ├── domain/
│   │   ├── money/
│   │   ├── wallet/
│   │   ├── wagering/
│   │   └── events/
│   ├── app/
│   │   ├── wallet/
│   │   ├── wagering/
│   │   └── reconciliation/
│   ├── infra/
│   │   ├── postgres/
│   │   ├── sqs/
│   │   ├── outbox/
│   │   └── auth/
│   ├── httpapi/
│   ├── observability/
│   └── fxmodules/
├── migrations/
│   ├── 000001_init.up.sql
│   ├── 000001_init.down.sql
│   └── 000002_constraints_triggers.up.sql
│   └── 000002_constraints_triggers.down.sql
├── deploy/
│   ├── keycloak/
│   │   └── realm-export.json
│   └── localstack/
│       └── init-queues.sh
├── test/
│   ├── integration/
│   └── support/
├── docs/
│   ├── CHALLENGE.md
│   └── IMPLEMENTATION_PLAN.md
├── docker-compose.yml
├── docker-compose.test.yml
├── docker-compose.multi.yml
├── Dockerfile
├── Makefile
├── .env.example
├── README.md
├── ARCHITECTURE.md
├── go.mod
└── go.sum
```

Regra de dependência:

```text
domain  <-  app  <-  infra/http
domain  <-  app  <-  infra/postgres
domain  <-  app  <-  infra/sqs
```

O pacote `domain` não importa Fx, HTTP, SQS, `pgx` ou qualquer biblioteca de infraestrutura.

---

## 4. Modelo de dados

### 4.1 `wallets`

| Coluna | Tipo | Regra |
| --- | --- | --- |
| `id` | `uuid` | PK, UUIDv7 |
| `player_id` | `uuid` | NOT NULL |
| `currency` | `char(3)` | NOT NULL |
| `balance_minor` | `bigint` | NOT NULL, `>= 0` |
| `version` | `bigint` | NOT NULL, `>= 1`, default 1 |
| `created_at` | `timestamptz` | NOT NULL |
| `updated_at` | `timestamptz` | NOT NULL |

Constraints:

```sql
UNIQUE (player_id, currency)
CHECK (balance_minor >= 0)
CHECK (version >= 1)
```

### 4.2 `wager_transactions`

| Coluna | Tipo | Regra |
| --- | --- | --- |
| `id` | `uuid` | PK, UUIDv7 |
| `origin` | `text` | `INTERNAL` ou `EXTERNAL` |
| `kind` | `text` | `OPENING`, `BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK` |
| `status` | `text` | `PENDING`, `PENDING_REFERENCE`, `PROCESSED`, `REJECTED`, `FAILED` |
| `provider_id` | `text` | NULL apenas para `OPENING` |
| `external_transaction_id` | `text` | NULL apenas para `OPENING` |
| `idempotency_key` | `text` | NULL apenas para `OPENING` |
| `payload_hash` | `text` | SHA-256 hexadecimal; NULL para `OPENING` |
| `internal_reference` | `text` | Identidade interna estável; preenchida apenas em `OPENING` |
| `wallet_id` | `uuid` | NOT NULL, FK |
| `player_id` | `uuid` | NOT NULL |
| `round_id` | `text` | NULL apenas para `OPENING` |
| `game_id` | `text` | NULL apenas para `OPENING` |
| `amount_minor` | `bigint` | NOT NULL |
| `currency` | `char(3)` | NOT NULL |
| `reference_external_transaction_id` | `text` | NULL |
| `reference_transaction_id` | `uuid` | NULL, FK para a própria tabela |
| `failure_code` | `text` | NULL quando não rejeitada/falha |
| `result_balance_minor` | `bigint` | Saldo observado no processamento |
| `result_currency` | `char(3)` | Moeda do saldo observado |
| `reference_attempts` | `int` | NOT NULL default 0 |
| `next_reference_at` | `timestamptz` | NULL |
| `created_at` | `timestamptz` | NOT NULL |
| `updated_at` | `timestamptz` | NOT NULL |
| `processed_at` | `timestamptz` | NULL |

Constraints e índices:

```sql
-- identidade externa
CREATE UNIQUE INDEX uq_tx_provider_external
  ON wager_transactions (provider_id, external_transaction_id)
  WHERE origin = 'EXTERNAL';

-- idempotência
CREATE UNIQUE INDEX uq_tx_provider_idempotency
  ON wager_transactions (provider_id, idempotency_key)
  WHERE origin = 'EXTERNAL';

-- abertura única por carteira e identidade interna estável
CREATE UNIQUE INDEX uq_tx_opening_wallet
  ON wager_transactions (wallet_id)
  WHERE kind = 'OPENING';

CREATE UNIQUE INDEX uq_tx_opening_internal_reference
  ON wager_transactions (internal_reference)
  WHERE kind = 'OPENING';

-- uma única reversão bem-sucedida por transação original
-- Corrida: duas reversões podem coexistir como PENDING_REFERENCE; a transição
-- para PROCESSED é protegida por este índice. O worker deve traduzir a violação
-- em DUPLICATE_REVERSAL.
CREATE UNIQUE INDEX uq_tx_single_reversal
  ON wager_transactions (reference_transaction_id)
  WHERE kind IN ('REFUND', 'ROLLBACK')
    AND status = 'PROCESSED';

-- consulta de pendências
CREATE INDEX ix_tx_pending_reference
  ON wager_transactions (next_reference_at)
  WHERE status = 'PENDING_REFERENCE';

-- consulta por provedor
CREATE INDEX ix_tx_provider_status
  ON wager_transactions (provider_id, status);

-- checks
CHECK (amount_minor >= 0)
CHECK (kind <> 'LOSS' OR amount_minor = 0)
CHECK (kind NOT IN ('BET', 'WIN', 'REFUND', 'ROLLBACK') OR amount_minor > 0)
CHECK (origin <> 'INTERNAL' OR kind = 'OPENING')
CHECK (kind <> 'OPENING' OR origin = 'INTERNAL')
CHECK (kind <> 'OPENING' OR internal_reference IS NOT NULL)
CHECK (kind <> 'OPENING' OR (
  provider_id IS NULL AND external_transaction_id IS NULL AND idempotency_key IS NULL
  AND payload_hash IS NULL AND round_id IS NULL AND game_id IS NULL
  AND reference_external_transaction_id IS NULL
))
CHECK (origin <> 'EXTERNAL' OR provider_id IS NOT NULL)
CHECK (origin <> 'EXTERNAL' OR external_transaction_id IS NOT NULL)
CHECK (origin <> 'EXTERNAL' OR idempotency_key IS NOT NULL)
CHECK (origin <> 'EXTERNAL' OR payload_hash IS NOT NULL)
CHECK (kind NOT IN ('REFUND', 'ROLLBACK') OR reference_external_transaction_id IS NOT NULL)
CHECK (status <> 'REJECTED' OR failure_code IS NOT NULL)
```

Observações:

- `OPENING` com saldo positivo nasce diretamente `PROCESSED`.
- `OPENING` com saldo zero não é criado.
- `LOSS` processada não cria ledger nem incrementa versão, mas grava `result_balance_minor`.
- `REJECTED`/`FAILED` não alteram saldo.
- A igualdade de valor da reversão é validada na aplicação dentro da transação; não é
  expressável como `CHECK` simples porque depende de outra linha.
- `internal_reference` é obrigatório apenas em `OPENING`.

### 4.3 `wallet_ledger_entries`

| Coluna | Tipo | Regra |
| --- | --- | --- |
| `id` | `uuid` | PK |
| `wallet_id` | `uuid` | NOT NULL, FK |
| `transaction_id` | `uuid` | NOT NULL, FK |
| `direction` | `text` | `DEBIT` ou `CREDIT` |
| `amount_minor` | `bigint` | NOT NULL, `> 0` |
| `currency` | `char(3)` | NOT NULL |
| `balance_before_minor` | `bigint` | NOT NULL, `>= 0` |
| `balance_after_minor` | `bigint` | NOT NULL, `>= 0` |
| `created_at` | `timestamptz` | NOT NULL |

Constraints:

```sql
UNIQUE (wallet_id, transaction_id)

CHECK (amount_minor > 0)
CHECK (balance_before_minor >= 0)
CHECK (balance_after_minor >= 0)

CHECK (
  (direction = 'CREDIT' AND balance_after_minor = balance_before_minor + amount_minor)
  OR
  (direction = 'DEBIT'  AND balance_after_minor = balance_before_minor - amount_minor)
)
```

Índice para paginação:

```sql
CREATE INDEX ix_ledger_wallet_cursor
  ON wallet_ledger_entries (wallet_id, created_at, id);
```

Triggers:

- Bloquear `UPDATE` e `DELETE` em `wallet_ledger_entries`.
- Validar por trigger que `transaction_id` pertence à mesma `wallet_id`.
- Validar por trigger que a moeda do ledger coincide com a moeda da carteira e da transação.
- Validar por trigger que `balance_before` e `balance_after` coincidem com a transação e o saldo.

### 4.4 `inbox_messages`

| Coluna | Tipo | Regra |
| --- | --- | --- |
| `id` | `bigserial` | PK |
| `consumer_name` | `text` | NOT NULL |
| `message_id` | `text` | NOT NULL |
| `payload_hash` | `text` | NOT NULL |
| `status` | `text` | `RECEIVED`, `COMPLETED`, `FAILED` |
| `received_at` | `timestamptz` | NOT NULL |
| `completed_at` | `timestamptz` | NULL |
| `last_error` | `text` | NULL |

```sql
UNIQUE (consumer_name, message_id)
INDEX (status, received_at)
```

### 4.5 `outbox_events`

| Coluna | Tipo | Regra |
| --- | --- | --- |
| `event_id` | `uuid` | PK, identidade estável |
| `aggregate_type` | `text` | Ex.: `wallet`, `wager_transaction` |
| `aggregate_id` | `text` | NOT NULL |
| `event_type` | `text` | NOT NULL |
| `event_version` | `int` | NOT NULL |
| `payload` | `jsonb` | Snapshot imutável |
| `correlation_id` | `text` | NOT NULL |
| `causation_id` | `text` | NULL |
| `occurred_at` | `timestamptz` | NOT NULL |
| `attempts` | `int` | NOT NULL default 0 |
| `next_attempt_at` | `timestamptz` | NOT NULL default now() |
| `lease_until` | `timestamptz` | NULL |
| `last_error` | `text` | NULL |
| `published_at` | `timestamptz` | NULL |
| `created_at` | `timestamptz` | NOT NULL |

```sql
CREATE INDEX ix_outbox_pending
  ON outbox_events (next_attempt_at, occurred_at)
  WHERE published_at IS NULL;
```

### 4.6 Imutabilidade e transições

Triggers obrigatórios:

1. `wallet_ledger_entries`: impedir `UPDATE` e `DELETE`.
2. `wager_transactions`: impedir alteração quando `status` atual é terminal
   (`PROCESSED`, `REJECTED`, `FAILED`).
3. `outbox_events`: impedir alteração de `event_id`, `event_type`, `payload` e `occurred_at`.
4. `wallets`: impedir `DELETE`; saldo e versão só mudam via operação transacional.

As triggers são defesa adicional. A aplicação também valida a máquina de estados.

---

## 5. Domínio

### 5.1 Money

API sugerida:

```go
type Money struct {
    minor    int64
    currency string
}

func Parse(amount, currency string) (Money, error)
func New(minor int64, currency string) (Money, error)
func Zero(currency string) (Money, error)
func (m Money) Amount() string
func (m Money) Minor() int64
func (m Money) Currency() string
func (m Money) IsZero() bool
func (m Money) IsPositive() bool
func (m Money) Add(other Money) (Money, error)
func (m Money) Sub(other Money) (Money, error)
func (m Money) Neg() (Money, error)
func (m Money) Compare(other Money) (int, error)
func (m Money) Equal(other Money) bool
func (m Money) MarshalJSON() ([]byte, error)
func (m *Money) UnmarshalJSON(data []byte) error
```

Erros:

```go
var (
    ErrInvalidAmount   = errors.New("invalid amount")
    ErrInvalidCurrency = errors.New("invalid currency")
    ErrCurrencyMismatch = errors.New("currency mismatch")
    ErrOverflow        = errors.New("money overflow")
)
```

Testes obrigatórios: parsing, escala, zero, negativos, overflow, underflow, `Neg` de `MinInt64`,
serialização, `UnmarshalJSON`, moedas incompatíveis, `NaN`, `Infinity`, notação científica.

### 5.2 Wallet

- Construtor de criação com validação.
- Construtor de reidratação que **não** reaplica movimentação nem emite evento.
- `Debit`, `Credit` com validação de saldo e moeda.
- Incremento de versão apenas quando o saldo muda.
- `LOSS` não chama `Debit`/`Credit` e não altera versão.
- Reidratação aceita valores persistidos e valida invariantes.

### 5.3 WagerTransaction

- Tipos: `OPENING`, `BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK`.
- Status: `PENDING`, `PENDING_REFERENCE`, `PROCESSED`, `REJECTED`, `FAILED`.
- Transições permitidas:
  - `PENDING -> PROCESSED | REJECTED | FAILED | PENDING_REFERENCE`
  - `PENDING_REFERENCE -> PROCESSED | REJECTED | FAILED`
  - terminais: sem transição.
- Métodos explícitos: `MarkProcessed`, `MarkRejected`, `MarkFailed`, `MarkPendingReference`.
- Construtor de criação e construtor de reidratação separados.
- Rejeitar `OPENING` vindo de HTTP/SQS.
- `LOSS` exige `Money` zero na moeda da carteira.

### 5.4 Erros de domínio

- Erros classificáveis com `errors.Is`/`errors.As`.
- `panic` proibido para rejeição de negócio.
- Erros de I/O recebem `context.Context` e respeitam cancelamento/timeout.

---

## 6. Fluxo transacional

### 6.1 Abertura de carteira (`POST /wallets`)

1. Autenticar como `internal`.
2. Validar `playerId` e `initialBalance`.
3. Abrir transação SQL.
4. Inserir `wallets` com `version = 1`.
5. Se `initialBalance > 0`:
   - gerar `internalReference` estável (UUIDv7 ou chave idempotente interna);
   - inserir `OPENING` `PROCESSED` com `internal_reference`;
   - inserir ledger de crédito (`balance_before = 0`, `balance_after = inicial`);
   - inserir outbox `WagerTransactionProcessed`;
   - inserir outbox `WalletBalanceChanged`.
6. Se `initialBalance == 0`: não criar `OPENING`, ledger nem eventos.
7. Commit.
8. Retornar `201`.
9. Conflito de `(playerId, currency)` retorna `409 WALLET_ALREADY_EXISTS`.
10. Reenvio do `POST /wallets` para a mesma `(playerId, currency)` retorna
    `409 WALLET_ALREADY_EXISTS`; o `internalReference` existe para identidade e auditoria
    interna e não é enviado pelo cliente.

### 6.2 Operação externa (HTTP/SQS)

1. Validar autenticação e `providerId`.
2. Validar payload e `Money`.
3. Normalizar campos e calcular `payloadHash`.
4. Abrir transação SQL.
5. Consultar replay:
   - por `(provider_id, idempotency_key)`;
   - por `(provider_id, external_transaction_id)`.
6. Em replay:
   - hash igual e chave igual: retornar resultado persistido + `idempotentReplay: true`;
   - transação ainda `PENDING`/`PENDING_REFERENCE`: retornar `202` com o status persistido,
     sem reaplicar nem reenfileirar;
   - chave igual e hash diferente: `409 IDEMPOTENCY_KEY_CONFLICT`;
   - identidade externa com chave diferente: `409 EXTERNAL_TRANSACTION_CONFLICT`.
7. `SELECT ... FOR UPDATE` na carteira.
8. Validar:
   - `walletId` existe e pertence ao `playerId` informado;
   - moeda da operação coincide com a da carteira;
   - provedor autenticado coincide com `providerId`;
   - `roundId` e `gameId` presentes para operações externas.
9. Resolver referência quando aplicável:
   - `REFUND`/`ROLLBACK`: referência obrigatória; lock da referência com `FOR UPDATE`;
   - `WIN`: referência opcional; se presente, deve ser `BET` `PROCESSED` da mesma rodada;
   - `REFUND`/`ROLLBACK` com referência ausente: criar `PENDING_REFERENCE`, outbox
     `WagerTransactionPendingReference`, commit, retornar `202`;
   - `WIN` sem referência: seguir para o processamento normal;
   - existente e não processada: manter pendente;
   - existente terminal sem sucesso: rejeitar com `REFERENCE_NOT_PROCESSED`;
   - existente processada: validar provedor, jogador, carteira, moeda, rodada e valor;
   - valor divergente: rejeitar com `REFERENCE_AMOUNT_MISMATCH`;
   - segunda reversão: rejeitar com `DUPLICATE_REVERSAL`.
10. Aplicar regra do tipo:
    - `BET`: debitar;
    - `WIN`: creditar (referência opcional não altera o valor);
    - `LOSS`: não movimentar;
    - `REFUND`: creditar valor da `BET` referenciada;
    - `ROLLBACK`: crédito se original for `BET`; débito se original for `WIN` ou `REFUND`.
11. Atualizar saldo e versão (exceto `LOSS` e rejeições).
12. Inserir ledger (exceto `LOSS` e rejeições).
13. Inserir outbox:
    - `WagerTransactionProcessed`;
    - `WalletBalanceChanged` quando houver mudança de saldo.
14. Marcar transação `PROCESSED`, `REJECTED` ou `FAILED`.
15. Commit.
16. Responder ou deletar mensagem SQS.

### 6.3 Regra de idempotência e concorrência

- O lock da carteira serializa operações da mesma carteira.
- Carteiras diferentes usam linhas diferentes e avançam em paralelo.
- Unique constraints resolvem corridas entre instâncias.
- Violação de unique de idempotência: rollback + releitura + comparação.
- Violação de unique de reversão: rejeitar `DUPLICATE_REVERSAL`; o worker trata a
  corrida entre duas reversões `PENDING_REFERENCE`.
- Violação de unique de abertura: `WALLET_ALREADY_EXISTS`.
- A referência de `REFUND`/`ROLLBACK` é lockada com `FOR UPDATE` após o lock da carteira.
- `OPENING` é idempotente pelo `internal_reference` e pela unicidade `(player_id, currency)`.
- HTTP e SQS concorrentes para a mesma operação convergem para o mesmo registro por
  `(provider_id, idempotency_key)` e `(provider_id, external_transaction_id)`.

### 6.4 Reconciliação

1. Abrir transação `REPEATABLE READ READ ONLY`.
2. Ler saldo armazenado da carteira.
3. Calcular `SUM(credits) - SUM(debits)` do ledger.
4. `difference = stored - calculated`.
5. Retornar `consistent`, `checkedEntries`, `storedBalance`, `calculatedBalance`, `difference`.
6. Registrar log de aviso e métrica quando divergente.
7. Nunca alterar saldo.

---

## 7. Contratos HTTP

### 7.1 Endpoints

| Método | Rota | Acesso |
| --- | --- | --- |
| `GET` | `/health/live` | Público |
| `GET` | `/health/ready` | Público |
| `POST` | `/wallets` | `internal` |
| `GET` | `/wallets/:walletId` | `internal` |
| `GET` | `/wallets/:walletId/ledger` | `internal` |
| `POST` | `/wallets/:walletId/reconciliation` | `internal` |
| `POST` | `/wagering/transactions` | `provider` |
| `GET` | `/wagering/transactions/:transactionId` | `internal` ou provider dono |
| `GET` | `/providers/:providerId/wagering/transactions/:externalTransactionId` | `internal` ou o próprio provider |
| `GET` | `/metrics` | `internal` (documentar) |

### 7.2 Regras de autorização

- `providerId` do body deve ser igual ao claim `provider_id`; divergência retorna `403`.
- Provider não acessa transação de outro provider.
- Replay também respeita o isolamento.
- Endpoints de carteira e reconciliação são internos.
- Token ausente, inválido ou expirado retorna `401`.
- Role insuficiente retorna `403`.

### 7.3 Códigos e corpos de erro

Formato:

```json
{
  "error": {
    "code": "INSUFFICIENT_FUNDS",
    "message": "insufficient funds",
    "transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
    "retryable": false
  }
}
```

| Código | HTTP | Significado |
| --- | ---: | --- |
| `INVALID_REQUEST` | 400 | Payload inválido |
| `INVALID_MONEY` | 400 | Valor monetário inválido |
| `IDEMPOTENCY_KEY_REQUIRED` | 400 | Header ausente |
| `IDEMPOTENCY_KEY_INVALID` | 400 | Chave vazia ou acima de 255 caracteres |
| `OPENING_NOT_ALLOWED` | 400 | `OPENING` via entrada externa |
| `LOSS_AMOUNT_NOT_ZERO` | 400 | `LOSS` com valor diferente de zero |
| `UNAUTHORIZED` | 401 | Token ausente/inválido/expirado |
| `FORBIDDEN_PROVIDER` | 403 | Provider divergente ou role insuficiente |
| `WALLET_NOT_FOUND` | 404 | Carteira inexistente |
| `TRANSACTION_NOT_FOUND` | 404 | Transação inexistente |
| `WALLET_ALREADY_EXISTS` | 409 | Duplicidade de carteira |
| `IDEMPOTENCY_KEY_CONFLICT` | 409 | Mesma chave, conteúdo diferente |
| `EXTERNAL_TRANSACTION_CONFLICT` | 409 | Mesma identidade externa, chave diferente |
| `INSUFFICIENT_FUNDS` | 422 | Aposta sem saldo |
| `INSUFFICIENT_FUNDS_FOR_REVERSAL` | 422 | Rollback sem saldo |
| `REFERENCE_REQUIRED` | 400 | Reversão sem referência |
| `REFERENCE_MISMATCH` | 422 | Provider/jogador/carteira/moeda/rodada divergentes |
| `REFERENCE_AMOUNT_MISMATCH` | 422 | Valor da reversão difere do referenciado |
| `WALLET_PLAYER_MISMATCH` | 422 | Carteira não pertence ao jogador informado |
| `REFERENCE_NOT_FOUND` | 422 | Referência inexistente após TTL |
| `REFERENCE_NOT_PROCESSED` | 422 | Referência terminal sem sucesso |
| `DUPLICATE_REVERSAL` | 422 | Reversão já aplicada |
| `INFRA_UNAVAILABLE` | 503 | Falha transitória de DB/SQS |
| `INTERNAL_ERROR` | 500 | Erro inesperado |

Regras:

- Rejeição de negócio é persistida como `REJECTED` e retorna `422` com `failureCode` estável.
- Falha permanente de infraestrutura é persistida como `FAILED` e retorna `422` com `failureCode`.
- `PENDING` e `PENDING_REFERENCE` retornam `202` com `transactionId`, `status` e sem `failureCode`.
- Falha transitória **não** deve ser persistida como rejeição.
- `retryable` é `true` apenas em `503`.

### 7.4 Replay

```json
{
  "transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
  "status": "PROCESSED",
  "balance": { "amount": "975.00", "currency": "BRL" },
  "idempotentReplay": true
}
```

O saldo retornado no replay é o saldo observado no processamento original
(`result_balance_minor`), mesmo que a carteira tenha mudado depois.

### 7.5 Paginação do ledger

- Cursor opaco: base64 de `created_at|id`.
- Ordenação estável: `created_at ASC, id ASC`.
- `limit` padrão 50, máximo 200.
- Resposta com `items` e `nextCursor`.

### 7.6 Consulta de transação

Resposta de `GET /wagering/transactions/:transactionId` e da rota por provedor:

```json
{
  "transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
  "providerId": "provider-a",
  "externalTransactionId": "transaction-123",
  "kind": "ROLLBACK",
  "status": "PENDING_REFERENCE",
  "failureCode": null,
  "money": { "amount": "25.00", "currency": "BRL" },
  "balance": { "amount": "975.00", "currency": "BRL" },
  "referenceExternalTransactionId": "transaction-100",
  "referenceTransactionId": null,
  "referenceAttempts": 3,
  "nextReferenceAt": "2026-09-08T12:05:00Z",
  "createdAt": "2026-09-08T12:00:00Z",
  "processedAt": null
}
```

- `PENDING`/`PENDING_REFERENCE` incluem `referenceAttempts` e `nextReferenceAt`.
- `REJECTED`/`FAILED` incluem `failureCode`.
- Para `OPENING`, campos externos e de referência são omitidos.

---

## 8. Contratos SQS

### 8.1 Filas

- `wager-transactions.fifo`
- `wager-transactions-dlq.fifo`
- `wager-events.fifo` (destino da outbox)

Configuração:

| Parâmetro | Valor |
| --- | --- |
| `FifoQueue` | `true` |
| `ContentBasedDeduplication` | `false` |
| `VisibilityTimeout` | 60s |
| `ReceiveMessageWaitTimeSeconds` | 20 |
| `MessageRetentionPeriod` | 4 dias |
| Redrive | DLQ com `maxReceiveCount = 5` |

- `MessageGroupId = walletId` para operações (serializa por carteira).
- `MessageDeduplicationId = idempotencyKey` na entrada.
- Outbox: `MessageGroupId = aggregateId`, `MessageDeduplicationId = eventId`.
- A aplicação **não** depende da deduplicação FIFO para integridade.

### 8.2 Consumo

1. Long poll.
2. Validar envelope.
3. Calcular hash do payload.
4. Abrir transação SQL.
5. Inserir inbox `(consumer_name, message_id)`.
6. Se já existe:
   - hash igual: commit e delete (já tratada);
   - hash diferente: enviar para DLQ e delete.
7. Executar o mesmo caso de uso do HTTP.
8. Marcar inbox `COMPLETED` na mesma transação do domínio.
9. Commit.
10. Delete da mensagem **somente após commit**.

### 8.3 Tratamento de falhas

| Situação | Ação |
| --- | --- |
| Sucesso | Commit + delete |
| Rejeição de negócio | Commit + delete |
| `PENDING_REFERENCE` | Commit + delete (worker assume) |
| Falha transitória | Rollback + não delete (reentrega) |
| Mensagem inválida permanente | Enviar para DLQ + delete |
| Tentativas esgotadas | DLQ via redrive |

### 8.4 Shutdown

- `SIGTERM` cancela o contexto raiz.
- Consumer para de buscar novas mensagens.
- Processamentos em andamento terminam dentro de um timeout configurável.
- Mensagens não concluídas têm visibilidade liberada (`ChangeMessageVisibility(0)`).
- Fx aguarda workers antes de fechar DB/SQS.

---

## 9. Outbox e eventos

### 9.1 Envelope

```json
{
  "eventId": "0192f2a0-0000-7000-8000-000000000001",
  "eventType": "WalletBalanceChanged",
  "eventVersion": 1,
  "aggregateId": "0192f291-27dd-7d3f-8071-5f8685deef37",
  "correlationId": "corr-123",
  "causationId": "0192f298-345e-7e38-af88-e43f851a819d",
  "occurredAt": "2026-09-08T12:00:00.000Z",
  "data": {}
}
```

### 9.2 Eventos obrigatórios

| Evento | Gatilho | Data mínima |
| --- | --- | --- |
| `WagerTransactionProcessed` | Operação `PROCESSED`, inclusive `LOSS` | `transactionId`, `providerId`, `externalTransactionId`, `kind`, `walletId`, `playerId`, `money`, `status` |
| `WagerTransactionRejected` | Rejeição definitiva | `transactionId`, `failureCode`, `kind`, `providerId`, `externalTransactionId` |
| `WalletBalanceChanged` | Mudança efetiva de saldo | `walletId`, `transactionId`, `direction`, `money`, `balanceBefore`, `balanceAfter`, `walletVersion` |
| `WagerTransactionPendingReference` | Espera por referência | `transactionId`, `referenceExternalTransactionId`, `attempts`, `nextAttemptAt` |

- Timestamps UTC em RFC 3339.
- Valores monetários em string decimal.
- `eventId` e `eventVersion` definidos pelo construtor.
- Payload é snapshot imutável.
- Em eventos de `OPENING`, `providerId`, `externalTransactionId`, `roundId` e `gameId`
  são omitidos, pois não se aplicam à origem interna.

### 9.3 Publisher

```sql
UPDATE outbox_events
SET lease_until = now() + interval '30 seconds',
    attempts = attempts + 1
WHERE event_id IN (
  SELECT event_id
  FROM outbox_events
  WHERE published_at IS NULL
    AND next_attempt_at <= now()
    AND (lease_until IS NULL OR lease_until < now())
  ORDER BY occurred_at
  FOR UPDATE SKIP LOCKED
  LIMIT $1
)
RETURNING *;
```

- Commit do claim antes de publicar.
- Publicar no SQS FIFO.
- Sucesso: `published_at = now()`, `lease_until = NULL`.
- Falha: `lease_until = NULL`, `next_attempt_at` com backoff, `last_error`.
- Crash entre claim e publicação: lease expira e outro publisher assume.
- Crash entre publicação e marcação: republica com o mesmo `eventId`; consumidores deduplicam.
- Múltiplos publishers são seguros por `SKIP LOCKED` + lease.
- Backoff: `min(2^attempts segundos, 5 minutos)`.

### 9.4 Destino

- Fila `wager-events.fifo`.
- Consumidores externos devem deduplicar por `eventId`.
- Documentar contratos no `ARCHITECTURE.md`.

---

## 10. Autenticação

### 10.1 Keycloak

- Realm `jungle-go`.
- Import automático via `deploy/keycloak/realm-export.json`.
- Clients:
  - `internal-service` (confidential, service account, role `internal`);
  - `provider-a` (confidential, service account, role `provider`, claim `provider_id`);
  - `provider-b` (idem).
- Audience `wager-api`.
- Sem usuários com senha para o fluxo principal.

### 10.2 Validação

- Baixar JWKS de `KEYCLOAK_JWKS_URL`.
- Cache com renovação em caso de `kid` desconhecido.
- Validar assinatura, `iss`, `aud`, `exp` e `nbf`.
- Extrair roles e `provider_id`.
- Middleware injeta principal no contexto.
- Erros de autenticação não expõem detalhes internos.

### 10.3 Fluxo de teste

```sh
curl -s -X POST "$KEYCLOAK_TOKEN_URL" \
  -d grant_type=client_credentials \
  -d client_id=provider-a \
  -d client_secret="$PROVIDER_A_SECRET"
```

Usar o `access_token` como `Authorization: Bearer ...`.

### 10.4 SQS

- O LocalStack provisiona IAM users e políticas de fila separadas para consumidor e produtores internos.
- O consumidor tem permissão apenas para `ReceiveMessage`, `DeleteMessage` e `ChangeMessageVisibility`
  na fila de transações e `SendMessage` na DLQ/fila de eventos.
- Provedores **não** recebem credenciais de escrita direta na fila; o canal suportado é HTTP.
- O consumidor valida `data.providerId` contra o cadastro de provedores, a carteira e o jogador.
- Interpretação adotada: SQS é canal interno confiável; a autorização por provedor no broker
  não é expressável apenas pelo corpo da mensagem.
- Documentar essa decisão e a limitação no `ARCHITECTURE.md`.

---

## 11. Observabilidade

### 11.1 Logs

- `log/slog` com handler JSON.
- Campos: `correlationId`, `messageId`, `transactionId`, `walletId`, `providerId`,
  `eventType`, `status`, `failureCode`, `attempt`.
- Não registrar tokens, credenciais, payloads financeiros completos ou dados sensíveis.
- `X-Correlation-Id` de entrada ou UUID gerado.
- Propagar `correlationId` para eventos da outbox.

### 11.2 Métricas

- `wager_transactions_total{status,kind}`
- `wager_idempotent_replays_total`
- `wager_idempotency_conflicts_total`
- `wager_sqs_messages_total{result}`
- `wager_sqs_retries_total`
- `wager_dlq_messages_total`
- `wager_outbox_pending`
- `wager_outbox_publish_latency_seconds`
- `wager_outbox_publish_failures_total`
- `wager_reference_pending`
- `wager_reference_attempts_total`
- `wager_concurrency_conflicts_total`
- `wager_reconciliation_divergences_total`
- `wager_request_duration_seconds{method,route,status}`

### 11.3 Health checks

- `GET /health/live`: retorna `200` se o processo está vivo.
- `GET /health/ready`: retorna `200` somente se:
  - PostgreSQL responde a `Ping` dentro de 2s;
  - SQS responde a `GetQueueAttributes` dentro de 2s.
- Em falha, retornar `503` com motivo genérico.
- Health checks são públicos e não expõem detalhes sensíveis.

---

## 12. Docker Compose e ambiente

### 12.1 Serviços

| Serviço | Imagem | Porta |
| --- | --- | --- |
| `postgres` | `postgres:16-alpine` | 5432 |
| `localstack` | `localstack/localstack:latest` | 4566 |
| `keycloak` | `quay.io/keycloak/keycloak:latest` | 8081 |
| `app` | build local | 8080 |
| `migrate` | build local (target migrate) | — |

Todos com healthcheck e `depends_on` com condição `service_healthy`.

### 12.2 Inicialização

- LocalStack: script em `/etc/localstack/init/ready.d/` cria as filas e o redrive.
- Keycloak: importa o realm no start.
- Migrations: serviço `migrate` executa `up` antes da aplicação.
- Aplicação valida configuração e dependências na inicialização.

### 12.3 Variáveis

```env
APP_ENV=local
APP_PORT=8080
DATABASE_URL=postgres://jungle:jungle@postgres:5432/jungle?sslmode=disable

AWS_REGION=us-east-1
AWS_ENDPOINT_URL=http://localstack:4566
AWS_ACCESS_KEY_ID=test
AWS_SECRET_ACCESS_KEY=test
SQS_TRANSACTIONS_QUEUE_URL=http://localstack:4566/000000000000/wager-transactions.fifo
SQS_TRANSACTIONS_DLQ_URL=http://localstack:4566/000000000000/wager-transactions-dlq.fifo
SQS_EVENTS_QUEUE_URL=http://localstack:4566/000000000000/wager-events.fifo

KEYCLOAK_ISSUER=http://keycloak:8081/realms/jungle-go
KEYCLOAK_JWKS_URL=http://keycloak:8081/realms/jungle-go/protocol/openid-connect/certs
KEYCLOAK_AUDIENCE=wager-api

REFERENCE_MAX_ATTEMPTS=10
REFERENCE_TTL=1h
OUTBOX_BATCH_SIZE=20
OUTBOX_LEASE=30s
SHUTDOWN_TIMEOUT=20s
```

### 12.4 Multi-instância

- `docker-compose.multi.yml` com `app-1`, `app-2`, `app-3`, portas 8080, 8082 e 8083.
- Cada instância com conexões e memória próprias.
- Todas apontam para o mesmo PostgreSQL e SQS.

---

## 13. Fases de implementação

### Fase 0 — Bootstrap

Entregáveis:

- `go.mod` com Go declarado.
- `cmd/app/main.go`.
- Configuração validada.
- Dockerfile multi-stage.
- `docker-compose.yml`.
- Inicialização de LocalStack e Keycloak.
- Makefile e `.env.example`.
- Estrutura de pastas.

Aceite:

- `docker compose up --build` sobe PostgreSQL, LocalStack, Keycloak e app.
- `/health/live` e `/health/ready` respondem.
- `go vet ./...` passa.

### Fase 1 — Money

Entregáveis:

- Pacote `internal/domain/money`.
- Testes de parsing, overflow, moedas e serialização.

Aceite:

- Nenhum `float32`/`float64` no fluxo financeiro.
- `go test ./internal/domain/money -race` passa.

### Fase 2 — Persistência

Entregáveis:

- Migrations up/down.
- Repositórios `pgx`.
- Triggers de imutabilidade e constraints.
- Testes de integração de schema.

Aceite:

- `migrate up` e `migrate down` funcionam.
- UPDATE/DELETE no ledger falham.
- Transição a partir de estado terminal falha.
- Unique constraints impedem duplicidade.

### Fase 3 — Carteiras

Entregáveis:

- Criação de carteira com `OPENING`, ledger e outbox.
- Leitura de carteira.
- Paginação do ledger.
- Reconciliação.

Aceite:

- Saldo inicial positivo cria `OPENING`, ledger e dois eventos.
- Saldo zero não cria `OPENING` nem eventos.
- Carteira duplicada retorna 409.
- Reconciliação retorna `consistent: true`.

### Fase 4 — Operações externas

Entregáveis:

- Caso de uso único para HTTP e SQS.
- `BET`, `WIN`, `LOSS`.
- Hash canônico e idempotência.
- Ledger e eventos.

Aceite:

- `BET` debita e incrementa versão.
- `WIN` credita e incrementa versão.
- `LOSS` não cria ledger e não incrementa versão.
- Replay retorna saldo original.
- Conflito de chave/conteúdo retorna 409.
- `OPENING` via HTTP é rejeitado.

### Fase 5 — Referências e reversões

Entregáveis:

- `REFUND` e `ROLLBACK`.
- Referência opcional do `WIN` (mesma rodada).
- Validação de referência: provedor, jogador, carteira, moeda, rodada e valor.
- `PENDING_REFERENCE` e worker com backoff.
- TTL/máximo de tentativas.
- Prevenção de reversão duplicada, inclusive em corrida.
- Replay de transação pendente.

Aceite:

- `REFUND` devolve o valor da `BET`.
- `ROLLBACK` de `BET` credita; de `WIN`/`REFUND` debita.
- Rollback sem saldo retorna `INSUFFICIENT_FUNDS_FOR_REVERSAL`.
- Valor divergente retorna `REFERENCE_AMOUNT_MISMATCH`.
- Referência de outro provedor/jogador/carteira/moeda/rodada retorna `REFERENCE_MISMATCH`.
- `WIN` com `BET` de outra rodada é rejeitado.
- Referência antecipada fica pendente e resolve depois.
- Replay de `PENDING_REFERENCE` retorna `202` sem reaplicar.
- Após TTL, rejeita com `REFERENCE_NOT_FOUND`.
- Segunda reversão retorna `DUPLICATE_REVERSAL`.

### Fase 6 — SQS e inbox

Entregáveis:

- Consumer SQS FIFO.
- Inbox na mesma transação.
- DLQ e redrive.
- Shutdown seguro.

Aceite:

- Reentrega não duplica movimentação.
- Mensagem só é removida após commit.
- Mensagem inválida chega à DLQ.
- Falha transitória não é persistida como rejeição.
- Falha permanente de infraestrutura registra `FAILED` e chega à DLQ.
- `SIGTERM` conclui ou libera mensagens.

### Fase 7 — Outbox

Entregáveis:

- Publisher concorrente com lease.
- Eventos tipados e serialização.
- Retry e backoff.

Aceite:

- Eventos só são publicados após commit.
- Dois publishers não publicam o mesmo evento simultaneamente.
- Crash após publicação republica com o mesmo `eventId`.
- Backoff funciona.

### Fase 8 — Autenticação

Entregáveis:

- Keycloak configurado.
- Middleware OIDC/JWKS.
- Autorização por role e `providerId`.
- Testes de isolamento.

Aceite:

- Token ausente/inválido/expirado retorna 401.
- Provider A não acessa transação do B.
- Endpoints internos bloqueiam provider.
- Replay respeita isolamento.

### Fase 9 — Observabilidade

Entregáveis:

- Logs JSON com correlação.
- Métricas Prometheus.
- Health checks.
- Reconciliação com log e métrica.

Aceite:

- Logs permitem rastrear `transactionId` e `walletId`.
- Métricas de status, retry, DLQ, outbox e reconciliação existem.
- Readiness reflete DB e SQS.

### Fase 10 — Testes e documentação

Entregáveis:

- Suíte unitária completa.
- Suíte de integração real.
- Testes de concorrência e recuperação.
- `README.md` da solução.
- `ARCHITECTURE.md`.
- `docs/CHALLENGE.md` preservando o enunciado.

Aceite:

- `go test ./...` passa.
- `go test -race ./...` passa.
- `go vet ./...` passa.
- Todos os cenários da matriz abaixo foram executados.

---

## 14. Matriz de testes

### 14.1 Unitários

| ID | Cenário | Pacote |
| --- | --- | --- |
| U01 | Parsing de `"25.00"` | money |
| U02 | Rejeição de vazio, `NaN`, `Infinity`, notação científica, vírgula e 3 casas | money |
| U03 | Rejeição de valores negativos externos | money |
| U04 | Overflow na soma, subtração e negação | money |
| U05 | Moedas incompatíveis em soma/comparação | money |
| U06 | Serialização e unmarshal com 2 casas | money |
| U07 | Criação de carteira e versão inicial | wallet |
| U08 | Débito sem saldo | wallet |
| U09 | `LOSS` sem ledger e sem versão | wagering |
| U10 | Transições válidas e inválidas | wagering |
| U11 | Rejeição de `OPENING` externo | wagering |
| U12 | Hash canônico igual para HTTP e SQS | app/wagering |
| U13 | Conflito de payload para a mesma chave | app/wagering |
| U14 | Construção de eventos e campos obrigatórios | events |
| U15 | Política de zero por tipo: `LOSS` = 0; `BET`/`WIN`/`REFUND`/`ROLLBACK` > 0; abertura zero não cria `OPENING` | wagering/wallet |
| U16 | `OPENING` interno com identidade estável, metadados e eventos corretos | wagering/events |
| U17 | `WIN` com referência de `BET` da mesma rodada e rejeição de rodada divergente | wagering |
| U18 | Reversão com valor diferente do referenciado | wagering |

### 14.2 Integração real

| ID | Cenário | Evidência |
| --- | --- | --- |
| I01 | Migrations up/down | Banco recriado |
| I02 | Constraints de saldo, moeda e ledger | INSERT/UPDATE falham |
| I03 | Imutabilidade do ledger | UPDATE/DELETE bloqueados |
| I04 | Abertura com saldo positivo | `OPENING`, ledger e 2 eventos |
| I05 | Abertura com saldo zero | Sem `OPENING`/ledger/eventos |
| I06 | Carteira duplicada | 409 |
| I07 | `BET`, `WIN` e `LOSS` | Saldo, ledger e eventos corretos |
| I08 | Replay HTTP | `idempotentReplay: true` e saldo original |
| I09 | Conflito de idempotência | 409 |
| I10 | `REFUND` e `ROLLBACK` | Movimentos corretos |
| I11 | Referência antecipada | `PENDING_REFERENCE` e resolução |
| I12 | Expiração de referência | `REJECTED` + evento |
| I13 | Reversão duplicada | `DUPLICATE_REVERSAL` |
| I14 | Inbox e reentrega SQS | Um único débito |
| I15 | DLQ | Mensagem inválida na DLQ |
| I16 | Outbox concorrente | Sem publicação duplicada antes do commit |
| I17 | Recuperação após restart | Idempotência e pendências preservadas |
| I18 | Fx lifecycle | Start/stop e liberação de recursos |
| I19 | Atomicidade financeira | Falha no meio da transação não deixa saldo/ledger/outbox parciais |
| I20 | Retry com backoff | SQS e outbox reentregam sem perder evento confirmado |
| I21 | Replay de `PENDING_REFERENCE` | Retorna 202 sem duplicar pendência |
| I22 | Evento de `OPENING` | Sem metadados externos e com `WalletBalanceChanged` correto |
| I23 | Broker policies | Consumidor/produtor com permissões mínimas no LocalStack |

### 14.3 Autenticação e autorização

| ID | Cenário |
| --- | --- |
| A01 | Token ausente |
| A02 | Token inválido |
| A03 | Token expirado |
| A04 | Provider A acessando transação do B |
| A05 | Provider acessando endpoint interno |
| A06 | Replay cruzado entre providers |
| A07 | Ausência de efeitos financeiros em acesso negado |

### 14.4 Concorrência e recuperação

| ID | Cenário | Resultado esperado |
| --- | --- | --- |
| C01 | Mesma aposta 50x em paralelo | 1 débito |
| C02 | 2 apostas de 80 sobre saldo 100 | 1 processada, 1 rejeitada, saldo 20, 1 débito |
| C03 | Carteiras distintas em paralelo | Ambas progridem |
| C04 | 3 instâncias independentes | Garantias preservadas |
| C05 | Kill após commit e antes do delete | Reentrega sem duplicidade |
| C06 | 2 publishers na mesma outbox | Recuperação sem perda |
| C07 | REFUND/ROLLBACK antes da referência | Resolução ou rejeição por TTL |
| C08 | Restart da aplicação | Idempotência e consistência preservadas |
| C09 | HTTP e SQS para a mesma operação | Um único efeito |
| C10 | Soma do ledger vs saldo armazenado | Iguais |
| C11 | Duas reversões concorrentes da mesma referência | Uma processada, uma `DUPLICATE_REVERSAL` |
| C12 | Duas aberturas concorrentes para o mesmo jogador/moeda | Uma criada, uma `WALLET_ALREADY_EXISTS` |

### 14.5 Comandos

```sh
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go test -tags=integration ./test/integration/...
```

### 14.6 Injeção de falhas

Para tornar C05/C06/C08 reproduzíveis, a aplicação expõe failpoints **somente em build de teste**,
ativados por variáveis de ambiente:

| Failpoint | Efeito |
| --- | --- |
| `FAILPOINT_SQS_AFTER_COMMIT` | Encerra o worker após o commit e antes do delete da mensagem |
| `FAILPOINT_OUTBOX_AFTER_PUBLISH` | Encerra o publisher após publicar e antes de marcar `published_at` |
| `FAILPOINT_PENDING_AFTER_COMMIT` | Encerra após persistir `PENDING_REFERENCE` |

- Implementação via interface `Failpoint` injetada no worker; em produção é um no-op.
- Alternativa complementar: `docker stop`/`kill -9` da instância durante o cenário.
- Os testes documentam qual failpoint usam e qual recuperação esperam.

---

## 15. Execução local

```sh
cp .env.example .env
docker compose up --build
```

Migrations:

```sh
docker compose run --rm migrate up
docker compose run --rm migrate down 1
```

Testes:

```sh
go test ./...
go test -race ./...
go vet ./...
```

Integração:

```sh
docker compose -f docker-compose.test.yml up -d
go test -tags=integration ./test/integration/...
docker compose -f docker-compose.test.yml down -v
```

Multi-instância:

```sh
docker compose -f docker-compose.yml -f docker-compose.multi.yml up --build
```

---

## 16. Interpretações adotadas

1. **Reversões:** no máximo uma reversão `PROCESSED` por transação original, mesmo entre
   `REFUND` e `ROLLBACK`. Isso impede devolução duplicada do mesmo débito.
2. **Endpoints de carteira:** restritos ao serviço interno. Providers recebem saldo nas
   respostas de operações e consultam apenas suas próprias transações.
3. **Operações síncronas:** `PENDING` genérico não é usado no fluxo normal. O único estado
   pendente durável é `PENDING_REFERENCE`, retomado por worker.
4. **SQS:** canal interno confiável; providers usam HTTP. O consumidor sempre valida o domínio.
5. **TTL de referência:** 10 tentativas ou 1 hora, configuráveis.
6. **Moeda:** apenas `BRL` nas entradas externas; o tipo `Money` suporta qualquer ISO 4217.
7. **`OPENING`:** criado exclusivamente por `POST /wallets`, nunca por HTTP/SQS externo.
8. **README:** o enunciado será preservado em `docs/CHALLENGE.md` e um novo `README.md`
   da solução será criado na fase de documentação.
9. **Métricas:** `/metrics` acessível apenas ao serviço interno; health checks públicos.
10. **Rejeição de negócio:** persistida como `REJECTED` e reproduzida em replay.
11. **`WIN`:** referência opcional restrita a `BET` da mesma rodada; não altera o valor.
12. **`FAILED`:** usado apenas para falha permanente de infraestrutura/auditoria; regra de
    negócio usa `REJECTED`.
13. **Valor de reversão:** precisa ser idêntico ao referenciado; reversões parciais não são aceitas.
14. **Hash canônico:** campos e normalizações definidos na seção 2.4.1; metadados de
    transporte nunca entram no cálculo.

---

## 17. Riscos e mitigação

| Risco | Mitigação |
| --- | --- |
| URL do issuer do Keycloak divergir entre host e containers | Configurar issuer e JWKS explicitamente; documentar |
| LocalStack com comportamento diferente da AWS | Testar filas, redrive e FIFO no Compose |
| Flakiness em testes de concorrência | Sincronização por canais/barreiras e timeouts generosos |
| Deadlocks em locks de carteira/referência | Ordem fixa de aquisição e retry limitado |
| Publicação duplicada de eventos | `eventId` estável + deduplicação no consumidor |
| Vazamento de dados entre providers | Autorização centralizada + testes negativos |
| Migrations down quebrarem triggers | Testar up/down em banco limpo |
| Shutdown deixar mensagens presas | Cancelar receive, finalizar in-flight e liberar visibilidade |
| Overflow em somas | Aritmética verificada e testes de limite |

---

## 18. Documentação obrigatória

### 18.1 `README.md` da solução

Deve conter:

- pré-requisitos;
- variáveis de ambiente;
- inicialização das filas;
- aplicação e reversão das migrations;
- execução da aplicação;
- exemplos de chamadas autenticadas;
- comandos de teste unitário, integração, `-race` e multi-instância;
- instruções de simulação de falha e failpoints.

### 18.2 `ARCHITECTURE.md`

Deve registrar, no mínimo:

- representação e limites de `Money`;
- delimitação das transações SQL entre repositórios;
- estratégia de idempotência e hash canônico;
- estratégia de locking e retry;
- tratamento de referências pendentes e TTL;
- política de reversões e exclusividade;
- inbox/outbox e recuperação de trabalho abandonado;
- autenticação, autorização e isolamento por provedor;
- uso do Fx e ordem de shutdown;
- distinção entre falhas transitórias, permanentes de negócio e permanentes de infraestrutura;
- limitações, interpretações adotadas e trabalho não concluído.

---

## 19. Definition of Done

### Integridade financeira
- [x] `Money` sem float e com overflow tratado.
- [x] Saldo nunca negativo.
- [x] Ledger append-only com constraints e triggers.
- [x] Reconciliação confiável.
- [x] Reversões sem duplicidade, com valor idêntico ao referenciado.
- [x] `ROLLBACK` com direção correta por tipo original.
- [x] `WIN` com referência opcional validada por rodada.
- [x] `OPENING` com identidade interna estável.

### Concorrência
- [x] Lock por carteira.
- [x] Sem lost updates.
- [x] 3 instâncias validadas.
- [x] Cenário 80/80 sobre 100 reproduzido.

### Idempotência
- [x] Persistente.
- [x] Replay retorna resultado original.
- [x] Replay de `PENDING_REFERENCE` retorna 202 sem reaplicar.
- [x] Conflito de chave/conteúdo.
- [x] HTTP e SQS equivalentes.
- [x] Campos e normalizações do hash canônico documentados.

### Mensageria e recuperação
- [x] Inbox na mesma transação.
- [x] Delete após commit.
- [x] DLQ e redrive.
- [x] Outbox com lease e retry.
- [x] Distinção entre transitório, `REJECTED` e `FAILED`.
- [x] Failpoints de C05/C06/C08 reproduzíveis.
- [x] Shutdown seguro.

### Arquitetura
- [x] Fx com módulos e lifecycle.
- [x] Domínio independente de infraestrutura.
- [x] Auth e autorização reais.
- [x] Isolamento entre providers.

### Testes
- [x] Unitários completos, incluindo zero por tipo e `OPENING` interno.
- [x] Integração com containers reais.
- [x] Atomicidade financeira e retry/backoff cobertos.
- [x] `-race` executado.
- [x] Cenários de falha e restart.

### Observabilidade
- [x] Logs JSON correlacionados.
- [x] Métricas obrigatórias.
- [x] Health checks.
- [x] Alerta de divergência de reconciliação.

### Documentação
- [x] `README.md` da solução.
- [x] `ARCHITECTURE.md` com todos os tópicos da seção 18.2.
- [x] `.env.example`.
- [x] `docs/CHALLENGE.md`.
- [x] Comandos reproduzíveis.
