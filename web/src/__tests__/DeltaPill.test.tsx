import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/preact';
import { DeltaPill } from '../components/DeltaPill';

describe('DeltaPill', () => {
  it('hides itself when deltaPct is undefined', () => {
    const { container } = render(<DeltaPill />);
    expect(container.firstChild).toBeNull();
  });

  it('renders up direction for positive delta > 1%', () => {
    render(<DeltaPill deltaPct={8.4} />);
    const pill = document.querySelector('.statnive-delta-pill');
    expect(pill?.className).toMatch(/is-up/);
    expect(screen.getByText('+8.4%')).toBeTruthy();
    expect(screen.getByText('↑')).toBeTruthy();
  });

  it('renders down direction for negative delta < -1%', () => {
    render(<DeltaPill deltaPct={-12.5} />);
    const pill = document.querySelector('.statnive-delta-pill');
    expect(pill?.className).toMatch(/is-down/);
    expect(screen.getByText('-12.5%')).toBeTruthy();
    expect(screen.getByText('↓')).toBeTruthy();
  });

  it('renders flat direction inside ±1% deadband', () => {
    render(<DeltaPill deltaPct={0.4} />);
    const pill = document.querySelector('.statnive-delta-pill');
    expect(pill?.className).toMatch(/is-flat/);
    expect(screen.getByText('+0.4%')).toBeTruthy();
  });

  it('appends vsLabel when provided', () => {
    render(<DeltaPill deltaPct={5.2} vsLabel="vs previous 7 days" />);
    expect(screen.getByText(/vs previous 7 days/i)).toBeTruthy();
  });

  it('sets an accessible aria-label', () => {
    const { container } = render(<DeltaPill deltaPct={5.2} vsLabel="vs previous 7 days" />);
    const wrapper = container.querySelector('.statnive-delta');
    expect(wrapper?.getAttribute('aria-label')).toMatch(/up \+5\.2% vs previous 7 days/i);
  });
});
