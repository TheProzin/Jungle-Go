import { useState } from 'react';
import { fetchToken } from '../lib/api';
import { IDENTITIES, type Identity } from '../lib/identities';

type Props = {
  identity: Identity;
  onIdentity: (identity: Identity) => void;
  token: string;
  onToken: (token: string) => void;
};

export function AuthBar({ identity, onIdentity, token, onToken }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const claims = decodeClaims(token);

  async function obtainToken() {
    setBusy(true);
    setError('');
    try {
      onToken(await fetchToken(identity));
    } catch (err) {
      setError(String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card">
      <h2>Autenticação</h2>
      <div className="row">
        <label>
          Identidade
          <select value={identity} onChange={(event) => onIdentity(event.target.value as Identity)}>
            {IDENTITIES.map((item) => (
              <option key={item.id} value={item.id}>
                {item.label}
              </option>
            ))}
          </select>
        </label>
        <button onClick={obtainToken} disabled={busy}>
          {busy ? 'Obtendo...' : 'Obter token'}
        </button>
        <button className="secondary" onClick={() => onToken('')} disabled={!token}>
          Limpar token
        </button>
      </div>
      <p className="hint">{IDENTITIES.find((item) => item.id === identity)?.hint}</p>
      {error && <p className="error">{error}</p>}
      {token && (
        <div className="token-box">
          <div>
            <strong>Token:</strong> {token.slice(0, 24)}...
          </div>
          {claims && (
            <div className="hint">
              roles: {String(claims.roles ?? '-')} | provider_id: {String(claims.provider_id ?? '-')} |
              exp: {claims.exp ? new Date(Number(claims.exp) * 1000).toLocaleString() : '-'}
            </div>
          )}
        </div>
      )}
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
