import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/preact';
import { LivePulse } from '../components/LivePulse';

describe('LivePulse', () => {
  it('renders a role=status element with default aria-label', () => {
    const { container } = render(<LivePulse />);
    const el = container.querySelector('.statnive-live-pulse');
    expect(el).toBeTruthy();
    expect(el?.getAttribute('role')).toBe('status');
    expect(el?.getAttribute('aria-label')).toBe('Live');
  });

  it('accepts a custom aria-label override', () => {
    const { container } = render(<LivePulse aria-label="Polling" />);
    const el = container.querySelector('.statnive-live-pulse');
    expect(el?.getAttribute('aria-label')).toBe('Polling');
  });
});
