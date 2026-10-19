# Jungle-Go — Processamento Distribuído de Apostas

Serviço em Go com Uber Fx para processar operações financeiras de provedores de jogos em ambiente distribuído.

O sistema oferece:

- API HTTP autenticada por Keycloak/OIDC
- Consumidor SQS FIFO com inbox transacional
- Ledger append-only com constraints e triggers
- Idempotência persistente
- Concorrência por carteira com locks pessimistas
- Reversões com referências pendentes
- Transactional outbox com publicação concorrente
- Métricas Prometheus, logs JSON e health checks

O enunciado original está preservado em [`docs/CHALLENGE.md`](docs/CHALLENGE.md).
As decisões técnicas estão em [`ARCHITECTURE.md`](ARCHITECTURE.md).
O plano de implementação está em [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md).

---

## Stack

| Responsabilidade | Tecnologia |
| --- | --- |
| Linguagem | Go 1.27 |
| Composição | Uber Fx |
| HTTP | `net/http` |
| Banco | PostgreSQL 16 + pgx/v5 |
| Migrations | golang-migrate com arquivos embutidos |
| Mensageria | AWS SDK v2 + LocalStack SQS FIFO |
| Autenticação | Keycloak OIDC + JWKS |
| Métricas | Prometheus |
| Ambiente | Docker Compose |

---

## Pré-requisitos

- Go 1.27+
- Docker 24+
- Docker Compose v2+
- Portas livres: `8080`, `8081`, `5432`, `4566`

---

## Início rápido

```sh
cp .env.example .env
docker compose up --build
```

O Compose inicia:

| Serviço | Porta |
| --- | --- |
| PostgreSQL | `5432` |
| Keycloak | `8081` |
| LocalStack SQS | `4566` |
| Aplicação | `8080` |

O serviço `migrate` executa as migrations automaticamente antes da aplicação.

Verificação:

```sh
curl http://localhost:8080/health/live
curl http://localhost:8080/health/ready
```

Resposta esperada do readiness:

```json
{"checks":{"postgres":"ok","sqs":"ok"},"status":"ready"}
```

Para encerrar:

```sh
docker compose down
```

Para remover também os volumes:

```sh
docker compose down -v
```

---

## Variáveis de ambiente

O arquivo [`.env.example`](.env.example) contém valores locais sem segredos reais.

Principais variáveis:

```env
APP_ENV=local
APP_PORT=8080
LOG_LEVEL=info

DATABASE_URL=postgres://jungle:jungle@localhost:5432/jungle?sslmode=disable

AWS_REGION=us-east-1
AWS_ENDPOINT_URL=http://localhost:4566
AWS_ACCESS_KEY_ID=test
AWS_SECRET_ACCESS_KEY=test

SQS_TRANSACTIONS_QUEUE_URL=http://localhost:4566/000000000000/wager-transactions.fifo
SQS_TRANSACTIONS_DLQ_URL=http://localhost:4566/000000000000/wager-transactions-dlq.fifo
SQS_EVENTS_QUEUE_URL=http://localhost:4566/000000000000/wager-events.fifo
ALLOWED_PROVIDERS=provider-a,provider-b

KEYCLOAK_ISSUER=http://localhost:8081/realms/jungle-go
KEYCLOAK_JWKS_URL=http://localhost:8081/realms/jungle-go/protocol/openid-connect/certs
KEYCLOAK_AUDIENCE=wager-api
```

Quando a aplicação roda dentro do Compose, o Compose usa:

```env
KEYCLOAK_ISSUER=http://localhost:8081/realms/jungle-go
KEYCLOAK_JWKS_URL=http://keycloak:8080/realms/jungle-go/protocol/openid-connect/certs
```

O issuer é o valor presente nos tokens emitidos para o host. O JWKS é obtido pela rede interna do Compose.

---

## Migrations

Aplicar:

```sh
docker compose run --rm migrate up
```

Reverter uma migration:

```sh
docker compose run --rm migrate down 1
```

Consultar versão:

```sh
docker compose run --rm migrate version
```

Alternativa local:

```sh
DATABASE_URL=postgres://jungle:jungle@localhost:5432/jungle?sslmode=disable \
  go run ./cmd/migrate up
```

Migrations disponíveis:

- `000001_init`
- `000002_constraints_triggers`

Cada migration possui `up` e `down`. A reversão é testada na suíte de integração.

---

## Filas SQS

O script [`deploy/localstack/init-queues.sh`](deploy/localstack/init-queues.sh) provisiona automaticamente:

- `wager-transactions.fifo`
- `wager-transactions-dlq.fifo`
- `wager-events.fifo`

Configuração principal:

| Parâmetro | Valor |
| --- | --- |
| FIFO | `true` |
| Visibility timeout da fila de entrada | 60s |
| Redrive | DLQ com `maxReceiveCount=5` |
| `MessageGroupId` de entrada | `walletId` |
| `MessageDeduplicationId` de entrada | `idempotencyKey` |
| `MessageGroupId` de eventos | `aggregateId` |
| `MessageDeduplicationId` de eventos | `eventId` |

Listar filas:

```sh
docker compose exec localstack awslocal sqs list-queues
```

A aplicação **não** depende da deduplicação do SQS para integridade. Unicidade e idempotência são garantidas no PostgreSQL.

### IAM (LocalStack)

O script de inicialização cria os usuários `wager-consumer` (consome a fila de
transações e publica eventos/DLQ) e `wager-producer` (publica transações) com
políticas de menor privilégio. No ambiente local a aplicação usa a credencial
raiz `test`/`test`; o IAM é documentado como contrato para execução em AWS.

Verificação:

```sh
docker compose exec localstack awslocal iam list-users
docker compose exec localstack awslocal iam list-user-policies --user-name wager-consumer
```

---

## Keycloak

O realm `jungle-go` é importado automaticamente a partir de
[`deploy/keycloak/realm-export.json`](deploy/keycloak/realm-export.json).

Clientes:

| Cliente | Role | Claim | Uso |
| --- | --- | --- | --- |
| `internal-service` | `internal` | — | Operações internas de carteira |
| `provider-a` | `provider` | `provider_id=provider-a` | Operações do provedor A |
| `provider-b` | `provider` | `provider_id=provider-b` | Operações do provedor B |

Segredos locais:

```text
internal-secret
provider-a-secret
provider-b-secret
```

Obter token interno:

```sh
INTERNAL_TOKEN=$(curl -s -X POST \
  http://localhost:8081/realms/jungle-go/protocol/openid-connect/token \
  -d grant_type=client_credentials \
  -d client_id=internal-service \
  -d client_secret=internal-secret \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')
```

Obter token de provedor:

```sh
PROVIDER_TOKEN=$(curl -s -X POST \
  http://localhost:8081/realms/jungle-go/protocol/openid-connect/token \
  -d grant_type=client_credentials \
  -d client_id=provider-a \
  -d client_secret=provider-a-secret \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')
```

### Regras de autorização

- `/health/live` e `/health/ready`: públicos.
- `/wallets`, ledger e reconciliação: somente role `internal`.
- `POST /wagering/transactions`: somente role `provider`.
- `providerId` do corpo deve ser igual ao `provider_id` do token.
- `GET /wagering/transactions/:id`: serviço interno ou provider dono.
- `GET /providers/:providerId/...`: serviço interno ou o próprio provider.
- `/metrics`: somente role `internal`.

Tokens ausentes, inválidos ou expirados retornam `401`.
Acesso indevido retorna `403`.

---

## Exemplos de uso

### 1. Abrir carteira

```sh
curl -s -X POST http://localhost:8080/wallets \
  -H "Authorization: Bearer $INTERNAL_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "initialBalance": {"amount": "1000.00", "currency": "BRL"}
  }'
```

Resposta:

```json
{
  "id": "0192f291-27dd-7d3f-8071-5f8685deef37",
  "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
  "balance": {"amount": "1000.00", "currency": "BRL"},
  "version": 1
}
```

### 2. Enviar BET

```sh
curl -s -X POST http://localhost:8080/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: provider-a:transaction-123' \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "transaction-123",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
    "roundId": "round-987",
    "gameId": "fortune-chimp",
    "kind": "BET",
    "money": {"amount": "25.00", "currency": "BRL"}
  }'
```

Resposta:

```json
{
  "transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
  "status": "PROCESSED",
  "balance": {"amount": "975.00", "currency": "BRL"},
  "idempotentReplay": false
}
```

Replay com a mesma chave e o mesmo corpo retorna o mesmo saldo com
`"idempotentReplay": true`.

### 3. Consultar

```sh
curl -s http://localhost:8080/wallets/$WALLET_ID \
  -H "Authorization: Bearer $INTERNAL_TOKEN"

curl -s "http://localhost:8080/wallets/$WALLET_ID/ledger?limit=50" \
  -H "Authorization: Bearer $INTERNAL_TOKEN"

curl -s -X POST http://localhost:8080/wallets/$WALLET_ID/reconciliation \
  -H "Authorization: Bearer $INTERNAL_TOKEN"

curl -s http://localhost:8080/wagering/transactions/$TRANSACTION_ID \
  -H "Authorization: Bearer $PROVIDER_TOKEN"
```

### 4. Reversões

`REFUND` e `ROLLBACK` exigem `referenceExternalTransactionId`:

```json
{
  "providerId": "provider-a",
  "externalTransactionId": "refund-123",
  "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
  "walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
  "roundId": "round-987",
  "gameId": "fortune-chimp",
  "kind": "REFUND",
  "money": {"amount": "25.00", "currency": "BRL"},
  "referenceExternalTransactionId": "transaction-123"
}
```

Se a referência ainda não existir, a resposta é `202` com
`"status": "PENDING_REFERENCE"`. O worker resolve automaticamente quando a
transação referenciada chegar.

### 5. SQS

Exemplo de mensagem:

```json
{
  "messageId": "msg-123",
  "type": "WagerTransactionRequested",
  "occurredAt": "2026-09-08T12:00:00.000Z",
  "data": {
    "providerId": "provider-a",
    "externalTransactionId": "transaction-sqs-123",
    "idempotencyKey": "provider-a:transaction-sqs-123",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
    "roundId": "round-987",
    "gameId": "fortune-chimp",
    "kind": "BET",
    "money": {"amount": "25.00", "currency": "BRL"}
  }
}
```

Envio local:

```sh
docker compose exec -T localstack awslocal sqs send-message \
  --queue-url http://localhost:4566/000000000000/wager-transactions.fifo \
  --message-group-id "$WALLET_ID" \
  --message-deduplication-id "provider-a:transaction-sqs-123" \
  --message-body "$MESSAGE_BODY"
```

O `messageId` do envelope é a identidade durável da inbox. Mensagens com o mesmo
`messageId` e o mesmo hash são deduplicadas. O mesmo `messageId` com hash
diferente é enviado para a DLQ.

### 6. Métricas

```sh
curl -s http://localhost:8080/metrics \
  -H "Authorization: Bearer $INTERNAL_TOKEN"
```

Métricas principais:

- `wager_transactions_total{status,kind}`
- `wager_idempotent_replays_total`
- `wager_idempotency_conflicts_total`
- `wager_concurrency_conflicts_total`
- `wager_sqs_messages_total{result}`
- `wager_sqs_retries_total`
- `wager_dlq_messages_total`
- `wager_outbox_pending`
- `wager_outbox_publish_latency_seconds`
- `wager_outbox_publish_failures_total`
- `wager_reference_pending`
- `wager_reference_attempts_total`
- `wager_reconciliation_divergences_total`
- `wager_request_duration_seconds{method,route,status}`

---

## Testes

### Unitários

```sh
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

### Integração

Dependências reais:

```sh
docker compose up -d postgres localstack keycloak
```

Executar:

```sh
go test -tags=integration ./test/integration/...
go test -race -tags=integration ./test/integration/...
```

Os testes de integração:

- criam bancos temporários no PostgreSQL;
- executam migrations up/down;
- criam filas FIFO temporárias no LocalStack;
- obtêm tokens reais no Keycloak;
- validam inbox, reentrega, DLQ, outbox concorrente, retries, auth e expiração de token.

Encerrar dependências:

```sh
docker compose down -v
```

---

## Múltiplas instâncias

O arquivo [`docker-compose.multi.yml`](docker-compose.multi.yml) sobe três instâncias independentes:

```sh
docker compose down
docker compose -f docker-compose.yml -f docker-compose.multi.yml up -d --build app-1 app-2 app-3
```

| Instância | Porta |
| --- | --- |
| `app-1` | `8090` |
| `app-2` | `8091` |
| `app-3` | `8092` |

Todas compartilham o mesmo PostgreSQL, SQS e Keycloak, mas possuem pool de conexões e memória próprios.

Para encerrar:

```sh
docker compose -f docker-compose.yml -f docker-compose.multi.yml down
```

---

## Front-end de teste

Há um console web simples em [`frontend/`](frontend/) para exercitar a API.

```sh
cd frontend
npm install
npm run dev
```

Acesse `http://localhost:5173`.

O dev server faz:

- proxy de `/api/*` para `http://localhost:8080`;
- troca de token `client_credentials` no servidor (`/token`), mantendo os
  segredos locais fora do browser.

O console cobre: autenticação, health, criação/consulta de carteira, ledger,
reconciliação, BET/WIN/LOSS/REFUND/ROLLBACK, consultas de transação, métricas e
simulação de mensagem SQS.

Build de produção:

```sh
npm run build
```

O simulador SQS usa o endpoint de desenvolvimento `POST /dev/sqs/send`
(autenticado como `internal`), que publica o envelope em
`wager-transactions.fifo`. Esse endpoint existe apenas para testes locais e não
deve ser exposto em produção.

---

## Simulações de falha

### PostgreSQL indisponível

```sh
docker compose stop postgres
curl -i http://localhost:8080/health/ready
docker compose start postgres
```

O readiness retorna `503` enquanto o banco estiver indisponível.

### SQS indisponível

```sh
docker compose stop localstack
docker compose start localstack
```

Mensagens não confirmadas são reentregues. Eventos pendentes da outbox permanecem no banco até a publicação.

### Referência antes da transação

Envie um `REFUND` ou `ROLLBACK` com uma referência inexistente. A resposta será
`202 PENDING_REFERENCE`. Envie depois a `BET` referenciada. O worker resolve a
pendência automaticamente.

### Reentrega de mensagem

Reenvie o mesmo envelope com um novo `MessageDeduplicationId` do SQS. A inbox
detecta o mesmo `messageId` e o mesmo hash e não reaplica a operação.

### Parada abrupta da aplicação

```sh
docker compose kill app
docker compose up -d app
```

Idempotência, pendências e outbox são preservadas no PostgreSQL.

### Outbox após commit

Pare o LocalStack após uma operação financeira. O evento permanece em
`outbox_events` com `published_at = NULL`. Ao reiniciar o LocalStack, o publisher
publica o evento com o mesmo `eventId`.

### Failpoints automatizados

Os cenários de crash entre commit e delete/publicação têm testes dedicados:

```sh
go test -tags=integration ./test/integration/... -run 'TestSQSCrashAfterCommit|TestOutboxCrashAfterPublish|TestPendingReferenceCrashAfterCommit' -v
```

---

## Estrutura do projeto

```text
cmd/app                 aplicação Fx
cmd/migrate             runner de migrations
internal/app            casos de uso
internal/domain         domínio puro
internal/httpapi        HTTP, auth middleware e observabilidade
internal/infra          PostgreSQL, SQS, auth, migrations
migrations              SQL versionado
deploy                  Keycloak e LocalStack
test/integration        testes com containers reais
docs                    enunciado e plano
```

O domínio não importa Fx, HTTP, SQS ou pgx.

---

## Limitações e decisões

- Apenas `BRL` é aceito nas entradas externas; `Money` suporta ISO 4217.
- `REFUND` e `ROLLBACK` são mutuamente exclusivos por transação original.
- O SQS é tratado como canal interno confiável; provedores usam HTTP. As credenciais e políticas do broker são documentadas no `ARCHITECTURE.md`.
- Não há OpenTelemetry, dashboards ou testes de carga (diferenciais opcionais).
- Não há ledger de partidas dobradas (opcional).
- A validação de expiração de token é feita pelo verificador OIDC e coberta por teste de integração real.
