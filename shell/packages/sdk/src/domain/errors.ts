export type DomainErrorPhase = "parse" | "evaluate";

// A malformed or unevaluable expression, distinct from one that evaluated to false.
export class DomainExpressionError extends Error {
  readonly phase: DomainErrorPhase;
  readonly expression: string;
  readonly position: number | undefined;

  constructor(phase: DomainErrorPhase, message: string, expression: string, position?: number) {
    super(position === undefined ? `domain: ${message}` : `domain: ${message} at position ${position}`);
    this.name = "DomainExpressionError";
    this.phase = phase;
    this.expression = expression;
    this.position = position;
  }
}
