import { useState } from 'react';
import { pretty, type ApiCall, type WalletRef } from '../lib/api';

export function Wallet({ call, onCreated }: { call: ApiCall; onCreated: (wallet: WalletRef) => void }) {
  const [playerId, setPlayerId] = useState<string>(crypto.randomUUID());
  const [amount, setAmount] = useState('100.00');
  const [walletId, setWalletId] = useState('');
  const [cursor, setCursor] = useState('');
  const [limit, setLimit] = useState('50');
  const [output, setOutput] = useState('');

  async function createWallet() {
    const result = await call('/wallets', {
      method: 'POST',
      body: {
        playerId,
        initialBalance: { amount, currency: 'BRL' },
      },
    });
    const body = result.body as { id?: string };
    if (result.status === 201 && body?.id) {
      setWalletId(body.id);
      onCreated({ id: body.id, playerId });
    }
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  async function getWallet() {
    const result = await call(`/wallets/${walletId}`);
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  async function getLedger() {
    const params = new URLSearchParams({ limit });
    if (cursor) {
      params.set('cursor', cursor);
    }
    const result = await call(`/wallets/${walletId}/ledger?${params.toString()}`);
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  async function reconcile() {
    const result = await call(`/wallets/${walletId}/reconciliation`, { method: 'POST' });
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  return (
    <section className="card">
      <h2>Carteira <span className="hint">(token internal)</span></h2>
      <p className="hint">A carteira criada é usada automaticamente em Operação e Simulador SQS.</p>
      <div className="grid">
        <label>
          playerId
          <input value={playerId} onChange={(event) => setPlayerId(event.target.value)} />
        </label>
        <label>
          Saldo inicial
          <input value={amount} onChange={(event) => setAmount(event.target.value)} />
        </label>
        <label>
          walletId
          <input value={walletId} onChange={(event) => setWalletId(event.target.value)} placeholder="retornado ao criar" />
        </label>
      </div>
      <div className="row">
        <button onClick={createWallet}>Criar carteira</button>
        <button onClick={getWallet} disabled={!walletId}>GET carteira</button>
      </div>
      <div className="row">
        <label>
          limit
          <input className="small" value={limit} onChange={(event) => setLimit(event.target.value)} />
        </label>
        <label>
          cursor
          <input value={cursor} onChange={(event) => setCursor(event.target.value)} placeholder="opcional" />
        </label>
        <button onClick={getLedger} disabled={!walletId}>GET ledger</button>
        <button onClick={reconcile} disabled={!walletId}>Reconciliar</button>
      </div>
      {output && <pre>{output}</pre>}
    </section>
  );
}
