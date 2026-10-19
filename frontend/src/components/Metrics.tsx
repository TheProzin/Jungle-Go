import { useState } from 'react';
import type { ApiCall } from '../lib/api';

export function Metrics({ call }: { call: ApiCall }) {
  const [output, setOutput] = useState('');

  async function load() {
    const result = await call('/metrics');
    setOutput(`${result.status}\n${result.raw}`);
  }

  return (
    <section className="card">
      <h2>Métricas <span className="hint">(token internal)</span></h2>
      <button onClick={load}>GET /metrics</button>
      {output && <pre className="metrics">{output}</pre>}
    </section>
  );
}
