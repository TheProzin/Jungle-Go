export type TransactionInput = {
  providerId: string;
  externalTransactionId: string;
  playerId: string;
  walletId: string;
  roundId: string;
  gameId: string;
  kind: string;
  amount: string;
  currency: string;
  referenceExternalTransactionId: string;
};

export function buildEnvelope(input: TransactionInput, messageId: string): Record<string, unknown> {
  return {
    messageId,
    type: 'WagerTransactionRequested',
    occurredAt: new Date().toISOString(),
    data: {
      providerId: input.providerId,
      externalTransactionId: input.externalTransactionId,
      idempotencyKey: `${input.providerId}:${input.externalTransactionId}`,
      playerId: input.playerId,
      walletId: input.walletId,
      roundId: input.roundId,
      gameId: input.gameId,
      kind: input.kind,
      money: { amount: input.amount, currency: input.currency },
      ...(input.referenceExternalTransactionId
        ? { referenceExternalTransactionId: input.referenceExternalTransactionId }
        : {}),
    },
  };
}
