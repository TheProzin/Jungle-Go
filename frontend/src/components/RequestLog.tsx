import type { LogEntry } from '../lib/api';

type Props = {
  entries: LogEntry[];
  onClear: () => void;
};

export function RequestLog({ entries, onClear }: Props) {
  function copyCurl(entry: LogEntry) {
    const parts = [`curl -s -X ${entry.method} http://localhost:8080${entry.path}`];
    if (entry.method !== 'GET') {
      parts.push(`-H 'Content-Type: application/json'`);
      parts.push(`-d '${entry.raw.replace(/'/g, `'\\''`)}'`);
    }
    parts.push(`-H "Authorization: Bearer $TOKEN"`);
    void navigator.clipboard.writeText(parts.join(' '));
  }

  return (
    <section className="card">
      <div className="row space-between">
        <h2>Requisições</h2>
        <button className="secondary" onClick={onClear} disabled={entries.length === 0}>Limpar</button>
      </div>
      {entries.length === 0 && <p className="hint">Nenhuma chamada ainda.</p>}
      <div className="log">
        {entries.map((entry) => (
          <details key={entry.id}>
            <summary>
              <span className={entry.status >= 400 ? 'bad' : 'good'}>{entry.status}</span>{' '}
              <strong>{entry.method}</strong> {entry.path}
              <span className="hint"> {new Date(entry.at).toLocaleTimeString()}</span>
            </summary>
            <div className="row">
              <span className="hint">correlationId: {entry.correlationId}</span>
              <button className="secondary" onClick={() => copyCurl(entry)}>Copiar cURL</button>
            </div>
            <pre>{entry.raw}</pre>
          </details>
        ))}
      </div>
    </section>
  );
}
