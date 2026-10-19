import { useState } from 'react';
import { pretty, type ApiCall } from '../lib/api';
import type { TransactionInput } from '../lib/envelope';

const KINDS = ['BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK'];

export function Transaction({ call }: { call: ApiCall }) {
  const [form, setForm] = useState<TransactionInput>({
    providerId: 'provider-a',
    externalTransactionId: `tx-${crypto.randomUUID()}`,
    playerId: crypto.randomUUID(),
    walletId: '',
    roundId: 'round-1',
    gameId: 'game-1',
    kind: 'BET',
    amount: '10.00',
    currency: 'BRL',
    referenceExternalTransactionId: '',
  });
  const [idempotencyKey, setIdempotencyKey] = useState<string>(`provider-a:${form.externalTransactionId}`);
  const [output, setOutput] = useState('');

  function update<K extends keyof TransactionInput>(key: K, value: TransactionInput[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  function regenerate() {
    const external = `tx-${crypto.randomUUID()}`;
    setForm((current) => ({ ...current, externalTransactionId: external }));
    setIdempotencyKey(`${form.providerId}:${external}`);
  }

  async function submit() {
    const body: Record<string, unknown> = {
      providerId: form.providerId,
      externalTransactionId: form.externalTransactionId,
      playerId: form.playerId,
      walletId: form.walletId,
      roundId: form.roundId,
      gameId: form.gameId,
      kind: form.kind,
      money: { amount: form.amount, currency: form.currency },
    };
    if (form.referenceExternalTransactionId) {
      body.referenceExternalTransactionId = form.referenceExternalTransactionId;
    }

    const result = await call('/wagering/transactions', {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      body,
    });
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  return (
    <section className="card">
      <h2>Operação <span className="hint">(token provider)</span></h2>
      <div className="grid">
        <label>
          providerId
          <input value={form.providerId} onChange={(event) => update('providerId', event.target.value)} />
        </label>
        <label>
          externalTransactionId
          <input value={form.externalTransactionId} onChange={(event) => update('externalTransactionId', event.target.value)} />
        </label>
        <label>
          playerId
          <input value={form.playerId} onChange={(event) => update('playerId', event.target.value)} />
        </label>
        <label>
          walletId
          <input value={form.walletId} onChange={(event) => update('walletId', event.target.value)} />
        </label>
        <label>
          roundId
          <input value={form.roundId} onChange={(event) => update('roundId', event.target.value)} />
        </label>
        <label>
          gameId
          <input value={form.gameId} onChange={(event) => update('gameId', event.target.value)} />
        </label>
        <label>
          kind
          <select value={form.kind} onChange={(event) => update('kind', event.target.value)}>
            {KINDS.map((kind) => (
              <option key={kind} value={kind}>{kind}</option>
            ))}
          </select>
        </label>
        <label>
          amount
          <input value={form.amount} onChange={(event) => update('amount', event.target.value)} />
        </label>
        <label>
          currency
          <input value={form.currency} onChange={(event) => update('currency', event.target.value)} />
        </label>
        <label>
          referenceExternalTransactionId
          <input
            value={form.referenceExternalTransactionId}
            onChange={(event) => update('referenceExternalTransactionId', event.target.value)}
            placeholder="REFUND/ROLLBACK"
          />
        </label>
        <label>
          Idempotency-Key
          <input value={idempotencyKey} onChange={(event) => setIdempotencyKey(event.target.value)} />
        </label>
      </div>
      <div className="row">
        <button onClick={submit} disabled={!form.walletId}>POST /wagering/transactions</button>
        <button className="secondary" onClick={regenerate}>Novo externalId</button>
      </div>
      {output && <pre>{output}</pre>}
    </section>
  );
}
