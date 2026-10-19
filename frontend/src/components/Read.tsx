import { useEffect, useState } from 'react';
import { pretty, type ApiCall, type AuthKind } from '../lib/api';
import type { Provider } from '../lib/identities';

export function Read({ call, provider }: { call: ApiCall; provider: Provider }) {
  const [transactionId, setTransactionId] = useState('');
  const [providerId, setProviderId] = useState<string>(provider);
  const [externalTransactionId, setExternalTransactionId] = useState('');
  const [authMode, setAuthMode] = useState<AuthKind>('provider');
  const [output, setOutput] = useState('');

  useEffect(() => {
    setProviderId(provider);
  }, [provider]);

  async function getById() {
    const result = await call(`/wagering/transactions/${transactionId}`, { auth: authMode });
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  async function getByProvider() {
    const result = await call(`/providers/${providerId}/wagering/transactions/${externalTransactionId}`, {
      auth: authMode,
    });
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  return (
    <section className="card">
      <h2>Consultas</h2>
      <p className="hint">
        Use token <strong>provider</strong> para validar isolamento (só a própria transação) ou
        <strong> internal</strong> para ler qualquer uma.
      </p>
      <div className="grid">
        <label>
          transactionId
          <input value={transactionId} onChange={(event) => setTransactionId(event.target.value)} />
        </label>
        <label>
          providerId
          <input value={providerId} onChange={(event) => setProviderId(event.target.value)} />
        </label>
        <label>
          externalTransactionId
          <input value={externalTransactionId} onChange={(event) => setExternalTransactionId(event.target.value)} />
        </label>
        <label>
          Token
          <select value={authMode} onChange={(event) => setAuthMode(event.target.value as AuthKind)}>
            <option value="provider">provider selecionado</option>
            <option value="internal">internal</option>
          </select>
        </label>
      </div>
      <div className="row">
        <button onClick={getById} disabled={!transactionId}>GET por transactionId</button>
        <button onClick={getByProvider} disabled={!externalTransactionId}>GET por provider + external</button>
      </div>
      {output && <pre>{output}</pre>}
    </section>
  );
}
