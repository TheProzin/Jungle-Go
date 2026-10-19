import { useCallback, useState } from 'react';
import { apiCall, type ApiCall, type LogEntry } from './lib/api';
import type { Identity } from './lib/identities';
import { AuthBar } from './components/AuthBar';
import { Health } from './components/Health';
import { Wallet } from './components/Wallet';
import { Transaction } from './components/Transaction';
import { Read } from './components/Read';
import { Metrics } from './components/Metrics';
import { SqsSimulator } from './components/SqsSimulator';
import { RequestLog } from './components/RequestLog';

export function App() {
  const [identity, setIdentity] = useState<Identity>('internal-service');
  const [token, setToken] = useState('');
  const [logs, setLogs] = useState<LogEntry[]>([]);

  const call: ApiCall = useCallback(
    async (path, options = {}) => {
      const result = await apiCall(path, { token, ...options });
      setLogs((current) =>
        [
          {
            id: crypto.randomUUID(),
            at: new Date().toISOString(),
            method: options.method ?? 'GET',
            path,
            status: result.status,
            correlationId: result.correlationId,
            raw: result.raw,
          },
          ...current,
        ].slice(0, 100),
      );
      return result;
    },
    [token],
  );

  return (
    <main>
      <header>
        <h1>Jungle-Go — Test Console</h1>
        <p className="hint">
          Use identidade <strong>internal-service</strong> para carteira, métricas e simulador SQS.
          Use <strong>provider-a</strong> ou <strong>provider-b</strong> para operações de jogo.
        </p>
      </header>

      <AuthBar identity={identity} onIdentity={setIdentity} token={token} onToken={setToken} />
      <Health call={call} />
      <Wallet call={call} />
      <Transaction call={call} />
      <Read call={call} />
      <SqsSimulator call={call} />
      <Metrics call={call} />
      <RequestLog entries={logs} onClear={() => setLogs([])} />
    </main>
  );
}
