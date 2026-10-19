import { useEffect, useState } from 'react';
import { pretty, type ApiCall, type WalletRef } from '../lib/api';
import type { Provider } from '../lib/identities';
import { buildEnvelope, type TransactionInput } from '../lib/envelope';

export function SqsSimulator({ call, provider, wallet }: { call: ApiCall; provider: Provider; wallet: WalletRef | null }) {
  const [form, setForm] = useState<TransactionInput>({
    providerId: 'provider-a',
    externalTransactionId: `sqs-${crypto.randomUUID()}`,
    playerId: crypto.randomUUID(),
    walletId: '',
    roundId: 'round-1',
    gameId: 'game-1',
    kind: 'BET',
    amount: '10.00',
    currency: 'BRL',
    referenceExternalTransactionId: '',
  });
  const [messageId, setMessageId] = useState<string>(`msg-${crypto.randomUUID()}`);
  const [output, setOutput] = useState('');

  useEffect(() => {
    setForm((current) => ({ ...current, providerId: provider }));
  }, [provider]);

  useEffect(() => {
    if (!wallet) {
      return;
    }
    setForm((current) => ({ ...current, playerId: wallet.playerId, walletId: wallet.id }));
  }, [wallet]);

  function update<K extends keyof TransactionInput>(key: K, value: TransactionInput[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  async function send() {
    const envelope = buildEnvelope(form, messageId);
    const result = await call('/dev/sqs/send', { method: 'POST', body: envelope });
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  return (
    <section className="card">
      <h2>Simulador SQS <span className="hint">(token internal)</span></h2>
      {!wallet && <p className="hint">Crie uma carteira na seção Carteira; playerId e walletId serão preenchidos automaticamente.</p>}
      <p className="hint">
        Publica o envelope em <code>wager-transactions.fifo</code>. O consumer processa de forma
        assíncrona; consulte a transação depois.
      </p>
      <div className="grid">
        <label>
          messageId
          <input value={messageId} onChange={(event) => setMessageId(event.target.value)} />
        </label>
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
            {['BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK'].map((kind) => (
              <option key={kind} value={kind}>{kind}</option>
            ))}
          </select>
        </label>
        <label>
          amount
          <input value={form.amount} onChange={(event) => update('amount', event.target.value)} />
        </label>
        <label>
          referenceExternalTransactionId
          <input
            value={form.referenceExternalTransactionId}
            onChange={(event) => update('referenceExternalTransactionId', event.target.value)}
            placeholder="REFUND/ROLLBACK"
          />
        </label>
      </div>
      <button onClick={send} disabled={!form.walletId}>Publicar na fila</button>
      {output && <pre>{output}</pre>}
    </section>
  );
}
