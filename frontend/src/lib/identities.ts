export type Identity = 'internal-service' | 'provider-a' | 'provider-b';
export type Provider = 'provider-a' | 'provider-b';

export const PROVIDERS: Array<{ id: Provider; label: string }> = [
  { id: 'provider-a', label: 'provider-a' },
  { id: 'provider-b', label: 'provider-b' },
];

export const IDENTITIES: Array<{ id: Identity; label: string; hint: string }> = [
  {
    id: 'internal-service',
    label: 'internal-service (internal)',
    hint: 'Carteiras, ledger, reconciliação, métricas e simulador SQS',
  },
  {
    id: 'provider-a',
    label: 'provider-a (provider)',
    hint: 'Operações do provedor A',
  },
  {
    id: 'provider-b',
    label: 'provider-b (provider)',
    hint: 'Operações do provedor B',
  },
];
