// Honest topology wording (constitution 6). Serendip doesn't verify high
// availability yet, so more nodes never claims it.
export function topologySentence(nodeCount: number): string {
  if (nodeCount <= 1) return "Single node. Nothing protects this install from a node failure.";
  return `${nodeCount} nodes registered. Serendip doesn't verify high availability yet, so don't assume a node failure is survivable.`;
}

/** The plain fact, for the Overview; the banner carries the warning. */
export function nodeCountSentence(nodeCount: number): string {
  if (nodeCount === 0) return "No nodes registered yet.";
  return nodeCount === 1 ? "1 node registered." : topologySentence(nodeCount);
}

export function isSingleNode(nodeCount: number): boolean {
  return nodeCount <= 1;
}
