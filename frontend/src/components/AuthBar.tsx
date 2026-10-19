import { PROVIDERS, type Provider } from '../lib/identities';

type Props = {
  provider: Provider;
  onProvider: (provider: Provider) => void;
  internalToken: string;
  providerToken: string;
  busy: boolean;
  error: string;
  onRefresh: () => void;
};

export function AuthBar({
  provider,
  onProvider,
  internalToken,
  providerToken,
  busy,
  error,
  onRefresh,
}: Props) {
  const providerClaims = decodeClaims(providerToken);

  return (
    <section className="card">
      <h2>Conexão com o Keycloak</h2>
      <p className="hint">
        Não é login de usuário. São clientes OAuth <code>client_credentials</code>:
        o front obtém automaticamente um token <strong>internal</strong> para carteira,
        métricas e simulador SQS, e um token <strong>provider</strong> para operações de jogo.
      </p>
      <div className="row">
        <label>
          Provider para operações
          <select value={provider} onChange={(event) => onProvider(event.target.value as Provider)}>
            {PROVIDERS.map((item) => (
              <option key={item.id} value={item.id}>{item.label}</option>
            ))}
          </select>
        </label>
        <button onClick={onRefresh} disabled={busy}>
          {busy ? 'Renovando...' : 'Renovar tokens'}
        </button>
      </div>
      <div className="badges">
        <span className={internalToken ? 'badge ok' : 'badge'}>
          internal: {internalToken ? 'conectado' : 'sem token'}
        </span>
        <span className={providerToken ? 'badge ok' : 'badge'}>
          {provider}: {providerToken ? 'conectado' : 'sem token'}
        </span>
      </div>
      {providerClaims && (
        <p className="hint">
          provider_id: {String(providerClaims.provider_id ?? '-')} | roles:{' '}
          {String(providerClaims.roles ?? '-')} | exp:{' '}
          {providerClaims.exp ? new Date(Number(providerClaims.exp) * 1000).toLocaleString() : '-'}
        </p>
      )}
      {error && <p className="error">{error}</p>}
    </section>
  );
}

function decodeClaims(token: string): Record<string, unknown> | null {
  if (!token) {
    return null;
  }
  try {
    const payload = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    const padded = payload + '='.repeat((4 - (payload.length % 4)) % 4);
    return JSON.parse(atob(padded)) as Record<string, unknown>;
  } catch {
    return null;
  }
}
