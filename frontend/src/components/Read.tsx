import { useState } from 'react';
import { pretty, type ApiCall } from '../lib/api';

export function Read({ call }: { call: ApiCall }) {
  const [transactionId, setTransactionId] = useState('');
  const [providerId, setProviderId] = useState('provider-a');
  const [externalTransactionId, setExternalTransactionId] = useState('');
  const [output, setOutput] = useState('');

  async function getById() {
    const result = await call(`/wagering/transactions/${transactionId}`);
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  async function getByProvider() {
    const result = await call(`/providers/${providerId}/wagering/transactions/${externalTransactionId}`);
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  return (
    <section className="card">
      <h2>Consultas <span className="hint">(internal ou provider dono)</span></h2>
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
      </div>
      <div className="row">
        <button onClick={getById} disabled={!transactionId}>GET por transactionId</button>
        <button onClick={getByProvider} disabled={!externalTransactionId}>GET por provider + external</button>
      </div>
      {output && <pre>{output}</pre>}
    </section>
  );
}
