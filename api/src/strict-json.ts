/** JSON.parse with duplicate object-key rejection at every nesting level. */
export function parseStrictJson(text: string): unknown {
  let index = 0;

  function fail(message: string): never {
    throw new SyntaxError(`${message} at byte ${index}`);
  }

  function whitespace(): void {
    while (index < text.length && /[\t\n\r ]/.test(text[index] ?? '')) index++;
  }

  function string(): string {
    if (text[index] !== '"') fail('expected string');
    const start = index++;
    while (index < text.length) {
      const character = text[index++];
      if (character === '"') {
        return JSON.parse(text.slice(start, index)) as string;
      }
      if (character === '\\') {
        if (index >= text.length) fail('unterminated escape');
        const escape = text[index++];
        if (escape === 'u') {
          if (!/^[0-9a-fA-F]{4}$/.test(text.slice(index, index + 4))) fail('invalid unicode escape');
          index += 4;
        } else if (!'"\\/bfnrt'.includes(escape ?? '')) {
          fail('invalid escape');
        }
      } else if (character !== undefined && character.charCodeAt(0) < 0x20) {
        fail('unescaped control character');
      }
    }
    fail('unterminated string');
  }

  function number(): number {
    const remaining = text.slice(index);
    const match = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(remaining);
    if (!match) fail('invalid number');
    index += match[0].length;
    const lexeme = match[0];
    const parsed = Number(lexeme);
    if (!Number.isFinite(parsed)) fail('number is outside the supported range');
    const rendered = JSON.stringify(parsed);
    const sourceNormalized = normalizeDecimal(lexeme);
    const renderedNormalized = normalizeDecimal(rendered);
    if (sourceNormalized !== renderedNormalized) {
      fail('number cannot be represented losslessly');
    }
    const exactInteger = normalizedInteger(sourceNormalized);
    if (exactInteger !== null && (!Number.isInteger(parsed) || BigInt(parsed) !== exactInteger)) {
      fail('number cannot be represented losslessly');
    }
    return parsed;
  }

  function array(): unknown[] {
    index++;
    const result: unknown[] = [];
    whitespace();
    if (text[index] === ']') {
      index++;
      return result;
    }
    while (true) {
      result.push(value());
      whitespace();
      if (text[index] === ']') {
        index++;
        return result;
      }
      if (text[index++] !== ',') fail('expected comma');
      whitespace();
    }
  }

  function object(): Record<string, unknown> {
    index++;
    const result = Object.create(null) as Record<string, unknown>;
    const keys = new Set<string>();
    whitespace();
    if (text[index] === '}') {
      index++;
      return result;
    }
    while (true) {
      const key = string();
      if (keys.has(key)) fail(`duplicate field ${JSON.stringify(key)}`);
      keys.add(key);
      whitespace();
      if (text[index++] !== ':') fail('expected colon');
      whitespace();
      result[key] = value();
      whitespace();
      if (text[index] === '}') {
        index++;
        return result;
      }
      if (text[index++] !== ',') fail('expected comma');
      whitespace();
    }
  }

  function value(): unknown {
    whitespace();
    switch (text[index]) {
      case '"': return string();
      case '{': return object();
      case '[': return array();
      case 't':
        if (text.slice(index, index + 4) !== 'true') fail('invalid value');
        index += 4;
        return true;
      case 'f':
        if (text.slice(index, index + 5) !== 'false') fail('invalid value');
        index += 5;
        return false;
      case 'n':
        if (text.slice(index, index + 4) !== 'null') fail('invalid value');
        index += 4;
        return null;
      default: return number();
    }
  }

  const result = value();
  whitespace();
  if (index !== text.length) fail('trailing JSON value');
  return result;
}

/** Canonical exact decimal form used only to compare a source lexeme with Number's rendering. */
function normalizeDecimal(lexeme: string): string {
  const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(lexeme);
  if (!match) throw new SyntaxError('invalid number');
  const fraction = match[3] ?? '';
  let digits = `${match[2]}${fraction}`.replace(/^0+/, '');
  if (digits === '') return '0';

  const exponentText = (match[4] ?? '0').replace(/^[+-]/, '').replace(/^0+/, '');
  // Any losslessly representable finite Number has a compact equivalent exponent.
  // Bounding the source exponent prevents adversarial multi-megabyte BigInt parsing.
  if (exponentText.length > 6) throw new SyntaxError('number exponent is outside the supported range');
  let exponent = BigInt(match[4] ?? '0') - BigInt(fraction.length);
  let significantEnd = digits.length;
  while (significantEnd > 0 && digits.charCodeAt(significantEnd - 1) === 0x30) significantEnd--;
  const trailingZeros = digits.length - significantEnd;
  if (trailingZeros > 0) {
    digits = digits.slice(0, significantEnd);
    exponent += BigInt(trailingZeros);
  }
  return `${match[1] === '-' ? '-' : ''}${digits}e${exponent}`;
}

function normalizedInteger(normalized: string): bigint | null {
  if (normalized === '0') return 0n;
  const match = /^(-?)(\d+)e(-?\d+)$/.exec(normalized);
  if (!match) throw new SyntaxError('invalid normalized number');
  const exponent = Number(match[3]);
  if (!Number.isSafeInteger(exponent) || exponent < 0) return null;
  return BigInt(`${match[1]}${match[2]}${'0'.repeat(exponent)}`);
}

export function canonicalJson(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(',')}]`;
  const object = value as Record<string, unknown>;
  return `{${Object.keys(object).sort().map((key) => `${JSON.stringify(key)}:${canonicalJson(object[key])}`).join(',')}}`;
}
