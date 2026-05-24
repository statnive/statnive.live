import type { JSX as PreactJSX } from 'preact';

// Cally web components are self-registered on first `import('cally')`.
// Declare the three custom elements we mount so Preact JSX type-checks
// kebab-case attrs and accepts the elements as intrinsic. We deliberately
// model only the attrs we set — Cally exposes more, but adding them only
// when we need them keeps the surface honest.
declare module 'preact' {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace JSX {
    interface CallyCommonAttrs {
      value?: string;
      min?: string;
      max?: string;
      'first-day-of-week'?: string;
      today?: string;
      locale?: string;
      'show-outside-days'?: boolean | '';
    }

    interface IntrinsicElements {
      'calendar-date': PreactJSX.HTMLAttributes<HTMLElement> & CallyCommonAttrs;
      'calendar-range': PreactJSX.HTMLAttributes<HTMLElement> & CallyCommonAttrs;
      'calendar-month': PreactJSX.HTMLAttributes<HTMLElement>;
    }
  }
}

export {};
