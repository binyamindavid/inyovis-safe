import { createElement } from 'react';

export function PrintedIdentifier({ value }) {
  return createElement('bdi', { dir: 'ltr', className: 'bidi-value', title: value }, value);
}
