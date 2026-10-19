import { useState } from 'react';
import { pretty, type ApiCall } from '../lib/api';

export function Health({ call }: { call: ApiCall }) {
  const [output, setOutput] = useState('');

  async function run(path: string) {
    const result = await call(path);
    setOutput(`${result.status}\n${pretty(result.body)}`);
  }

  return (
    <section className="card">
      <h2>Health</h2>
      <div className="row">
        <button onClick={() => run('/health/live')}>GET /health/live</button>
        <button onClick={() => run('/health/ready')}>GET /health/ready</button>
      </div>
      {output && <pre>{output}</pre>}
    </section>
  );
}
