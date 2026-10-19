import { useCallback, useEffect, useRef, useState } from 'react';
import {
  apiCall,
  fetchToken,
  type ApiCall,
  type AuthKind,
  type LogEntry,
  type WalletRef,
} from './lib/api';
import type { Provider } from './lib/identities';
import { AuthBar } from './components/AuthBar';
import { Health } from './components/Health';
import { Wallet } from './components/Wallet';
import { Transaction } from './components/Transaction';
import { Read } from './components/Read';
import { Metrics } from './components/Metrics';
import { SqsSimulator } from './components/SqsSimulator';
import { RequestLog } from './components/RequestLog';

export function App() {
  const [provider, setProvider] = useState<Provider>('provider-a');
  const [internalToken, setInternalToken] = useState('');
  const [providerToken, setProviderToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [wallet, setWallet] = useState<WalletRef | null>(null);

  const tokens = useRef({ internal: '', provider: '' });
  const providerRef = useRef<Provider>(provider);
  providerRef.current = provider;

  const refresh = useCallback(async () => {
    setBusy(true);
    setError('');
    try {
      const [internal, providerValue] = await Promise.all([
        fetchToken('internal-service'),
        fetchToken(providerRef.current),
      ]);
      tokens.current = { internal, provider: providerValue };
      setInternalToken(internal);
      setProviderToken(providerValue);
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [provider, refresh]);

  const ensureToken = useCallback(async (kind: AuthKind): Promise<string> => {
    if (kind === 'none') {
      return '';
    }
    if (kind === 'internal') {
      if (tokens.current.internal) {
        return tokens.current.internal;
      }
      const token = await fetchToken('internal-service');
      tokens.current.internal = token;
      setInternalToken(token);
      return token;
    }
    if (tokens.current.provider) {
      return tokens.current.provider;
    }
    const token = await fetchToken(providerRef.current);
    tokens.current.provider = token;
    setProviderToken(token);
    return token;
  }, []);

  const call: ApiCall = useCallback(
    async (path, options = {}) => {
      const kind = options.auth ?? authForPath(path);
      const token = options.token ?? (await ensureToken(kind));
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
    [ensureToken],
  );

  function changeProvider(next: Provider) {
    tokens.current.provider = '';
    setProviderToken('');
    setProvider(next);
  }

  return (
    <main>
      <header>
        <h1>Jungle-Go — Test Console</h1>
        <p className="hint">
          O token correto é escolhido automaticamente por endpoint: <strong>internal</strong> para
          carteira/métricas/SQS e <strong>provider</strong> para operações de jogo.
        </p>
      </header>

      <AuthBar
        provider={provider}
        onProvider={changeProvider}
        internalToken={internalToken}
        providerToken={providerToken}
        busy={busy}
        error={error}
        onRefresh={refresh}
      />
      <Health call={call} />
      <Wallet call={call} onCreated={setWallet} />
      <Transaction call={call} provider={provider} wallet={wallet} />
      <Read call={call} provider={provider} />
      <SqsSimulator call={call} provider={provider} wallet={wallet} />
      <Metrics call={call} />
      <RequestLog entries={logs} onClear={() => setLogs([])} />
    </main>
  );
}

function authForPath(path: string): AuthKind {
  if (path.startsWith('/health')) {
    return 'none';
  }
  if (path.startsWith('/wagering') || path.startsWith('/providers')) {
    return 'provider';
  }
  return 'internal';
}
