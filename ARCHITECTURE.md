# Arquitetura — Jungle-Go

Este documento registra as decisões técnicas da solução, as fronteiras
transacionais, as garantias financeiras e as limitações conhecidas.

---

## 1. Visão geral

```text
                    ┌────────────────────┐
   Provider ───────▶│ HTTP API (net/http)│
                    │ Keycloak JWT       │
                    └─────────┬──────────┘
                              │
                    ┌─────────▼──────────┐
                    │ Caso de uso        │
                    │ wagering / wallet  │
                    └─────────┬──────────┘
                              │
          ┌───────────────────┼───────────────────┐
          │                   │                   │
   ┌──────▼──────┐     ┌──────▼──────┐     ┌──────▼──────┐
   │ PostgreSQL  │     │ SQS entrada │     │ Outbox      │
   │ wallets     │     │ inbox       │     │ publisher   │
   │ ledger      │     │ DLQ         │     │ SQS eventos │
   │ outbox      │     └─────────────┘     └─────────────┘
   └─────────────┘
```

Componentes:

- API HTTP autenticada por OIDC.
- Caso de uso único compartilhado por HTTP e SQS.
- PostgreSQL como fonte de verdade.
- Inbox transacional para SQS.
- Outbox transacional para eventos de integração.
- Worker de referências pendentes.
- Publisher concorrente da outbox.

---

## 2. Dinheiro

Representação:

```go
type Money struct {
    minor    int64
    currency string
}
```

- Unidade mínima: centavos.
- Moeda: código ISO 4217 validado.
- Escala fixa: 2 casas.
- Limite: `±92_233_720_368_547_758.07`.
- Parsing estrito de string decimal.
- Sem `float32`/`float64` em nenhuma etapa.
- Soma, subtração e negação com verificação de overflow.
- Valores negativos permitidos apenas em cálculos internos.
- Entradas financeiras externas rejeitam valores negativos.
- Persistência em `BIGINT` de centavos e `CHAR(3)` para a moeda.

Serialização externa:

```json
{"amount":"25.00","currency":"BRL"}
```

---

## 3. Persistência e transações SQL

Biblioteca: `pgx/v5` com SQL explícito.

A camada de aplicação delimita a transação através de `ports.UnitOfWork`:

```go
uow.Do(ctx, func(repos ports.Repositories) error {
    // carteira, transação, ledger, outbox, inbox
    return nil
})
```

Repositórios não abrem transações. Eles recebem a instância transacional e
executam SQL. Isso mantém a fronteira transacional visível no caso de uso.

Transações de leitura usam `REPEATABLE READ READ ONLY` para reconciliação e
consultas consistentes.

Tabelas principais:

- `wallets`
- `wager_transactions`
- `wallet_ledger_entries`
- `inbox_messages`
- `outbox_events`

Constraints e triggers impõem:

- saldo não negativo;
- moeda consistente;
- unicidade de carteira por jogador/moeda;
- unicidade de identidade externa;
- unicidade de chave de idempotência;
- uma abertura por carteira;
- no máximo uma reversão processada por transação original;
- ledger append-only;
- imutabilidade de transações terminais;
- imutabilidade da identidade/payload da outbox.

---

## 4. Concorrência por carteira

Estratégia: lock pessimista por carteira.

Fluxo de escrita:

1. `SELECT ... FOR UPDATE` na carteira.
2. Validações de jogador, moeda e referência.
3. Aplicação do débito/crédito em memória.
4. Inserção da transação final.
5. Inserção do ledger.
6. `UPDATE wallets` condicionado à versão esperada.
7. Inserção de eventos na outbox.

Garantias:

- Operações da mesma carteira serializam.
- Carteiras diferentes usam linhas diferentes e progridem em paralelo.
- Não há lock global.
- A versão da carteira só é incrementada quando o saldo muda.
- O `UPDATE` usa `WHERE id = $1 AND version = $2`.
- Violações de unicidade do banco são a última linha de defesa.

Retry e deadlocks:

- A estratégia escolhida é lock pessimista com ordem fixa: carteira primeiro,
  depois referência.
- Não há retry automático de `40001`/`40P01` nesta versão. Falhas transitórias
  retornam `503` no HTTP e mensagens SQS são reentregues.
- Operações concorrentes que perdem a corrida de unicidade são resolvidas por
  releitura e replay.

---

## 5. Idempotência

Identidades persistidas:

- `(provider_id, external_transaction_id)`
- `(provider_id, idempotency_key)`

Regras:

- mesma chave + mesmo hash: replay do resultado persistido;
- mesma chave + hash diferente: conflito;
- mesma identidade externa + chave diferente: conflito;
- replay de operação concluída devolve o saldo observado no processamento
  original, não o saldo atual.

Hash canônico:

- SHA-256 hexadecimal.
- Campos de negócio em ordem alfabética:
  - `externalTransactionId`
  - `gameId`
  - `kind`
  - `money.amount`
  - `money.currency`
  - `playerId`
  - `providerId`
  - `referenceExternalTransactionId`
  - `roundId`
  - `walletId`
- Excluídos: `Idempotency-Key`, headers, `messageId`, `occurredAt` e metadados
  de transporte.
- Amount normalizado em 2 casas; moeda em maiúsculas.
- Referência ausente e nula produzem o mesmo hash.
- HTTP e SQS usam a mesma função de fingerprint.

---

## 6. Máquina de estados

```text
PENDING ──────────────▶ PROCESSED
   │                    (terminal)
   ├───────────────────▶ REJECTED
   │                    (terminal)
   ├───────────────────▶ FAILED
   │                    (terminal)
   └──▶ PENDING_REFERENCE ──▶ PROCESSED
                            ├─▶ REJECTED
                            └─▶ FAILED
```

- `PROCESSED`: operação bem-sucedida.
- `REJECTED`: rejeição de negócio persistida.
- `FAILED`: falha permanente de infraestrutura registrada para auditoria.
- `PENDING_REFERENCE`: aguardando referência.
- Estados terminais não sofrem novas transições.
- Replay consulta o resultado persistido sem reaplicar a operação.

O agregado inicia em `PENDING`. Operações sem dependência são persistidas já no
estado final, sem commit intermediário, conforme permitido pelo enunciado.
`FAILED` está modelado e disponível para falhas permanentes registradas; no
fluxo atual, mensagens permanentemente inválidas são enviadas à DLQ e a inbox é
marcada como `FAILED`.

---

## 7. Regras das operações

| Tipo | Movimento | Regras |
| --- | --- | --- |
| `BET` | Débito | valor positivo, saldo suficiente |
| `WIN` | Crédito | valor positivo, referência opcional a `BET` da mesma rodada |
| `LOSS` | Nenhum | valor `0.00`, sem ledger, sem mudança de versão |
| `REFUND` | Crédito | referencia `BET` processada, valor igual ao referenciado |
| `ROLLBACK` | Contrário | referencia `BET`, `WIN` ou `REFUND` processada |

`ROLLBACK`:

| Original | Direção |
| --- | --- |
| `BET` | crédito |
| `WIN` | débito |
| `REFUND` | débito |

Rejeições:

- `INSUFFICIENT_FUNDS` para aposta sem saldo.
- `INSUFFICIENT_FUNDS_FOR_REVERSAL` para rollback sem saldo.
- `REFERENCE_NOT_FOUND`, `REFERENCE_NOT_PROCESSED`, `REFERENCE_MISMATCH`,
  `REFERENCE_AMOUNT_MISMATCH` e `DUPLICATE_REVERSAL`.

---

## 8. Referências pendentes

Quando a referência ainda não existe:

1. A transação é persistida como `PENDING_REFERENCE`.
2. Um evento `WagerTransactionPendingReference` é gravado na outbox.
3. O worker de referências assume a continuidade.

Worker:

- `SELECT ... FOR UPDATE SKIP LOCKED` em `next_reference_at <= now()`.
- Backoff exponencial configurável.
- Limite por tentativas ou TTL.
- Referência encontrada e processada: aplica a reversão.
- Referência ainda pendente: reagenda.
- Referência terminal sem sucesso: rejeita com `REFERENCE_NOT_PROCESSED`.
- Referência inexistente após o limite: rejeita com `REFERENCE_NOT_FOUND`.

Configuração:

```env
REFERENCE_MAX_ATTEMPTS=10
REFERENCE_TTL=1h
REFERENCE_INITIAL_BACKOFF=1s
REFERENCE_MAX_BACKOFF=5m
REFERENCE_WORKER_INTERVAL=1s
```

---

## 9. Reversões

- `REFUND` e `ROLLBACK` são mutuamente exclusivos por transação original.
- O índice único parcial `uq_tx_single_reversal` garante, no máximo, uma
  reversão `PROCESSED` por `reference_transaction_id`.
- A transação referenciada é lockada com `FOR UPDATE` após o lock da carteira.
- A verificação de reversão existente ocorre sob o lock da carteira/referência.
- O valor da reversão deve ser igual ao valor referenciado.
- A operação e a referência devem concordar em provedor, jogador, carteira,
  moeda e rodada.
- Correções adicionais exigiriam novos lançamentos corretivos.

---

## 10. Ledger

Cada lançamento contém:

- `walletId`
- `transactionId`
- direção (`DEBIT`/`CREDIT`)
- valor
- saldo anterior
- saldo posterior
- instante

Invariantes:

- `balanceAfter = balanceBefore ± amount`;
- saldos não negativos;
- moeda igual à da carteira e da transação;
- `UNIQUE(wallet_id, transaction_id)`;
- `UPDATE` e `DELETE` bloqueados por trigger.

`LOSS` e rejeições não geram lançamentos.

---

## 11. Inbox

Tabela `inbox_messages`:

- unicidade `(consumer_name, message_id)`;
- hash canônico do payload;
- status `RECEIVED`, `COMPLETED`, `FAILED`;
- `received_at`, `completed_at`, `last_error`.

Fluxo SQS:

1. Parse do envelope.
2. Inserção da inbox na mesma transação do domínio.
3. Processamento do caso de uso.
4. Conclusão da inbox na mesma transação.
5. Commit.
6. Delete da mensagem SQS.

Reentrega:

- mesmo `messageId` + mesmo hash: duplicata, confirma e não reaplica;
- mesmo `messageId` + hash diferente: falha permanente e DLQ.

---

## 12. Outbox

Eventos são gravados na mesma transação de estado, saldo, ledger e inbox.

Publisher:

- claim com `FOR UPDATE SKIP LOCKED`;
- lease de 30s;
- envio para `wager-events.fifo`;
- `MessageGroupId = aggregateId`;
- `MessageDeduplicationId = eventId`;
- `published_at` somente após envio;
- falhas recebem backoff e `last_error`;
- lease expirado permite que outra instância assuma;
- crash entre envio e confirmação republica com o mesmo `eventId`.

Envelope:

```json
{
  "eventId": "...",
  "eventType": "WalletBalanceChanged",
  "version": 1,
  "aggregateId": "...",
  "correlationId": "...",
  "causationId": "...",
  "occurredAt": "...",
  "data": {}
}
```

Eventos:

- `WagerTransactionProcessed`
- `WagerTransactionRejected`
- `WalletBalanceChanged`
- `WagerTransactionPendingReference`

---

## 13. Consumidor SQS

- FIFO.
- Long polling.
- `messageId` do envelope como identidade durável.
- `MessageGroupId = walletId` e `MessageDeduplicationId = idempotencyKey` na
  entrada.
- Delete apenas após commit.
- Rejeições de negócio e `PENDING_REFERENCE` são confirmados.
- Falhas transitórias usam `ChangeMessageVisibility` com backoff.
- Mensagens inválidas e hash divergente vão para a DLQ.
- `SIGTERM` para de buscar e conclui ou libera o trabalho em andamento.

A aplicação não depende da deduplicação FIFO para integridade.

---

## 14. Autenticação e autorização

IdP: Keycloak.

Fluxo: `client_credentials`.

Tokens JWT validados por JWKS:

- assinatura;
- issuer;
- audience `wager-api`;
- `exp` e `nbf`.

Claims:

- `roles`: `internal` ou `provider`;
- `provider_id`.

Isolamento:

- carteira, ledger e reconciliação: `internal`;
- operações externas: `provider`;
- `providerId` do corpo deve igualar a identidade autenticada;
- provider só lê suas próprias transações;
- serviço interno pode ler todas.

SQS:

- credenciais e políticas do broker são restritas a produtores/consumidores
  internos;
- validações de domínio continuam no consumidor;
- provedores usam HTTP como canal de entrada;
- `ALLOWED_PROVIDERS` define o registry aceito pelo consumidor; provider
  desconhecido é falha permanente e vai para a DLQ.
- O LocalStack provisiona os usuários IAM `wager-consumer` (consome a fila de
  transações e publica eventos/DLQ) e `wager-producer` (publica transações) com
  políticas de menor privilégio em `deploy/localstack/init-queues.sh`. No
  ambiente local a aplicação roda com a credencial raiz `test`/`test`, portanto
  o IAM não é *enforçado*; em AWS real a aplicação usaria as credenciais do
  `wager-consumer`.

### 14.1 Contratos HTTP e códigos

Corpo de erro:

```json
{
  "error": {
    "code": "INSUFFICIENT_FUNDS",
    "message": "insufficient funds",
    "retryable": false
  }
}
```

| Status | Casos |
| --- | --- |
| `200` | operação processada ou replay |
| `201` | carteira criada |
| `202` | `PENDING` / `PENDING_REFERENCE` |
| `400` | `INVALID_REQUEST`, `INVALID_MONEY`, `IDEMPOTENCY_KEY_REQUIRED`, `IDEMPOTENCY_KEY_INVALID`, `OPENING_NOT_ALLOWED`, `LOSS_AMOUNT_NOT_ZERO`, `REFERENCE_REQUIRED` |
| `401` | `UNAUTHORIZED` |
| `403` | `FORBIDDEN_PROVIDER` |
| `404` | `WALLET_NOT_FOUND`, `TRANSACTION_NOT_FOUND` |
| `409` | `WALLET_ALREADY_EXISTS`, `IDEMPOTENCY_KEY_CONFLICT`, `EXTERNAL_TRANSACTION_CONFLICT` |
| `422` | `INSUFFICIENT_FUNDS`, `INSUFFICIENT_FUNDS_FOR_REVERSAL`, `REFERENCE_MISMATCH`, `REFERENCE_AMOUNT_MISMATCH`, `WALLET_PLAYER_MISMATCH`, `REFERENCE_NOT_FOUND`, `REFERENCE_NOT_PROCESSED`, `DUPLICATE_REVERSAL`, `TRANSACTION_FAILED` |
| `503` | `INFRA_UNAVAILABLE` |
| `500` | `INTERNAL_ERROR` |

Resposta de processamento:

```json
{
  "transactionId": "...",
  "status": "PROCESSED",
  "balance": {"amount": "975.00", "currency": "BRL"},
  "idempotentReplay": false
}
```

Em rejeição, `balance` é omitido e `failureCode` é incluído. Em pendência, o status é
`PENDING_REFERENCE` e a resposta é `202`.

O ledger segue paginação por cursor opaco (`nextCursor`) e a reconciliação devolve
`storedBalance`, `calculatedBalance`, `difference`, `consistent` e `checkedEntries`.

---

## 15. Fx e ciclo de vida

Módulos Fx:

- configuração;
- observabilidade;
- PostgreSQL;
- autenticação;
- repositórios;
- casos de uso;
- HTTP;
- SQS;
- workers.

`fx.Lifecycle`:

- valida configuração na inicialização;
- abre e valida o pool;
- inicia servidor HTTP;
- inicia consumer SQS;
- inicia worker de referências;
- inicia publisher da outbox;
- no shutdown, cancela workers, aguarda término e fecha dependências.

A ordem de parada é inversa à de inicialização. O pool é fechado depois dos
componentes que o utilizam.

---

## 16. Classificação de falhas

| Tipo | Exemplo | Tratamento |
| --- | --- | --- |
| Transitória | DB/SQS indisponível, timeout, deadlock | rollback/não-ack, retry |
| Negócio | saldo, referência, moeda, duplicidade | `REJECTED` + `failureCode` |
| Infraestrutura permanente | payload irrecuperável, corrupção | `FAILED` / DLQ |

Falhas transitórias não são persistidas como rejeição.

O estado transacional `FAILED` permanece modelado e testado (`TestMarkFailed`),
porém não é produzido em runtime: falhas permanentes de mensagem são tratadas na
DLQ e a inbox é marcada `FAILED`.

Failpoints de teste em `internal/platform/failpoint` permitem simular:

- crash após commit e antes do delete da mensagem SQS;
- crash após publicação e antes de `published_at` na outbox;
- crash após commit de `PENDING_REFERENCE` e antes da continuidade.

Em produção os hooks são no-op; são habilitados apenas nos testes de integração.

---

## 17. Observabilidade

Logs JSON com:

- `correlationId`
- `messageId`
- `transactionId`
- `walletId`
- `providerId`
- `eventId`
- `eventType`
- `status`
- `failureCode`

Não são registrados tokens, credenciais, payloads financeiros completos ou dados
sensíveis.

Métricas Prometheus:

- resultados por status/tipo;
- replays;
- conflitos de idempotência;
- conflitos de concorrência;
- mensagens SQS por resultado;
- retries;
- DLQ;
- outbox pendente, latência e falhas;
- referências pendentes e tentativas;
- divergências de reconciliação;
- latência HTTP por rota/status.

Health checks:

- `/health/live`: processo vivo;
- `/health/ready`: PostgreSQL e SQS.

Métricas: `/metrics`, restrito a `internal`.

---

## 18. Reconciliação

- Transação `REPEATABLE READ READ ONLY`.
- Saldo armazenado versus soma de créditos menos débitos do ledger.
- `difference = stored - calculated`.
- Divergência gera log, métrica e resposta `consistent=false`.
- A reconciliação nunca altera saldo.

---

## 19. Múltiplas instâncias

Todas as instâncias compartilham PostgreSQL, SQS e Keycloak.

Coordenação:

- lock por carteira no banco;
- unicidade de idempotência no banco;
- inbox por `(consumer, messageId)`;
- outbox com `SKIP LOCKED` e lease;
- referências com `SKIP LOCKED`;
- consumer SQS distribuído.

Nenhuma instância é necessária para o funcionamento correto das demais.

---

## 20. Interpretações e limitações

1. Apenas `BRL` é aceito nas entradas externas; o tipo `Money` suporta ISO 4217.
2. `REFUND` e `ROLLBACK` são mutuamente exclusivos por transação original.
3. O SQS é tratado como canal interno confiável; provedores usam HTTP.
4. Não há ledger de partidas dobradas.
5. Não há OpenTelemetry, dashboards ou testes de carga.
6. O issuer do Keycloak é o endereço público usado na emissão; o JWKS é obtido
   pelo endereço interno do Compose.
7. Operações normais são síncronas; o único estado pendente durável é
   `PENDING_REFERENCE`.
8. A expiração de token é validada pelo verificador OIDC e coberta por teste de
   integração real com Keycloak.

---

## 21. Evidências de teste

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go test -tags=integration ./test/integration/...`
- `go test -race -tags=integration ./test/integration/...`

Cenários cobertos:

- Money, overflow e moedas;
- constraints e triggers;
- carteira, ledger, outbox e reconciliação;
- BET/WIN/LOSS;
- idempotência, replay e conflitos;
- concorrência de 20 operações iguais;
- REFUND/ROLLBACK e referências pendentes;
- inbox, reentrega, DLQ e hash divergente;
- outbox concorrente, retry e publicação real no LocalStack;
- autenticação real, isolamento entre providers e token expirado;
- multi-instância documentada.
